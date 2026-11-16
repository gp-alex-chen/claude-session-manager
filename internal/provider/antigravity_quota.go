package provider

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

type antigravityQuotaBucket struct {
	RemainingFraction *float64 `json:"remaining_fraction,omitempty"`
	ResetTime         string   `json:"reset_time,omitempty"`
	ResetInSeconds    *float64 `json:"reset_in_seconds,omitempty"`
	Disabled          bool     `json:"disabled,omitempty"`
}

// Quota is optional metadata. A malformed bucket must never discard task or
// approval state, and amount-only balances cannot establish a percentage.
func sanitizeAntigravityQuota(raw json.RawMessage) json.RawMessage {
	var buckets map[string]json.RawMessage
	if json.Unmarshal(raw, &buckets) != nil || len(buckets) > 128 {
		return nil
	}
	clean := map[string]antigravityQuotaBucket{}
	for id, value := range buckets {
		if id == "" || len(id) > 128 || strings.IndexFunc(id, unicode.IsControl) >= 0 {
			continue
		}
		var b antigravityQuotaBucket
		if json.Unmarshal(value, &b) != nil || b.Disabled || b.RemainingFraction == nil {
			continue
		}
		f := *b.RemainingFraction
		if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1 {
			continue
		}
		if reset, err := time.Parse(time.RFC3339Nano, b.ResetTime); err == nil && reset.Unix() > 0 {
			b.ResetTime = reset.UTC().Format(time.RFC3339Nano)
		} else {
			b.ResetTime = ""
		}
		if seconds := b.ResetInSeconds; seconds != nil && (math.IsNaN(*seconds) || math.IsInf(*seconds, 0) || *seconds < 0 || *seconds > 365*24*60*60) {
			b.ResetInSeconds = nil
		}
		clean[id] = b
	}
	if len(clean) == 0 {
		return nil
	}
	data, _ := json.Marshal(clean)
	return data
}

func antigravityQuota(s antigravitySnapshot) QuotaSnapshot {
	q := QuotaSnapshot{Provider: Antigravity, Status: "unavailable", Windows: []QuotaWindow{}, Message: "尚未收到可用的终端额度回调。请打开 Antigravity，在终端运行 /usage 后读取额度。"}
	if s.QuotaObservedAt <= 0 {
		return q
	}
	q.ReadAt = time.UnixMilli(s.QuotaObservedAt).UTC()
	var buckets map[string]antigravityQuotaBucket
	if json.Unmarshal(sanitizeAntigravityQuota(s.Quota), &buckets) != nil {
		return q
	}
	ids := make([]string, 0, len(buckets))
	for id := range buckets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		b := buckets[id]
		reset, _ := time.Parse(time.RFC3339Nano, b.ResetTime)
		var resetsAt int64
		if !reset.IsZero() {
			resetsAt = reset.Unix()
		} else if b.ResetInSeconds != nil {
			resetsAt = q.ReadAt.Unix() + int64(*b.ResetInSeconds)
		}
		scope, label, minutes := antigravityQuotaWindowIdentity(id)
		q.Windows = append(q.Windows, QuotaWindow{ID: id, Label: label, Scope: scope, WindowMinutes: minutes, UsedPercent: (1 - *b.RemainingFraction) * 100, ResetsAt: resetsAt})
	}
	if len(q.Windows) > 0 {
		q.Status, q.Plan = "available", s.PlanTier
		q.Message = "来自最近更新的 Antigravity 终端回调；运行 /usage 后可读取新额度。仅显示原生回调提供的百分比。"
	}
	return q
}

// These four identities were verified against the official CLI's quota
// callback. Unknown buckets keep their own identity rather than guessing a
// model group or a duration from a similar-looking name.
func antigravityQuotaWindowIdentity(id string) (scope, label string, minutes int64) {
	switch id {
	case "gemini-5h":
		return "Gemini", "Gemini · 5 小时", 300
	case "gemini-weekly":
		return "Gemini", "Gemini · 周", 10080
	case "3p-5h":
		return "第三方", "第三方 · 5 小时", 300
	case "3p-weekly":
		return "第三方", "第三方 · 周", 10080
	default:
		return "bucket:" + id, id, 0
	}
}

func (p *AntigravityAdapter) AccountQuota(ctx context.Context) (QuotaSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return QuotaSnapshot{}, err
	}
	_, _ = p.Status(ctx) // Local snapshots only; never starts a process or turn.
	p.mu.Lock()
	defer p.mu.Unlock()
	selected := antigravityQuota(antigravitySnapshot{})
	selectedToken := ""
	for token, runtime := range p.runtimes {
		q := runtime.quota
		if q.Status != "available" {
			continue
		}
		if q.ReadAt.After(selected.ReadAt) || (q.ReadAt.Equal(selected.ReadAt) && token < selectedToken) {
			selected, selectedToken = q, token
		}
	}
	selected.Windows = append([]QuotaWindow{}, selected.Windows...)
	if selected.Status == "available" && p.now().Sub(selected.ReadAt) >= 15*time.Minute {
		selected.Stale = true
		selected.Message = "终端额度回调已超过 15 分钟；请在 Antigravity 运行 /usage 后读取新额度。"
	}
	return selected, nil
}
