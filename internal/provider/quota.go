package provider

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Account quota is independent of a transcript's accumulated token usage.
// Missing windows mean unavailable; a genuine zero utilization is preserved.
type QuotaWindow struct {
	ID            string  `json:"id"`
	Label         string  `json:"label"`
	UsedPercent   float64 `json:"usedPercent"`
	WindowMinutes int64   `json:"windowMinutes"`
	ResetsAt      int64   `json:"resetsAt,omitempty"`
	Scope         string  `json:"scope,omitempty"`
}
type ResetCredit struct {
	ID          string `json:"id"`
	ResetType   string `json:"resetType"`
	Status      string `json:"status"`
	GrantedAt   int64  `json:"grantedAt"`
	ExpiresAt   *int64 `json:"expiresAt"`
	Title       string `json:"title"`
	Description string `json:"description"`
}
type ResetCreditsSummary struct {
	AvailableCount int64         `json:"availableCount"`
	Credits        []ResetCredit `json:"credits"`
}

type QuotaSnapshot struct {
	Provider     ID                   `json:"provider"`
	ResetCredits *ResetCreditsSummary `json:"resetCredits,omitempty"`
	AccountKey   string               `json:"-"`
	Status       string               `json:"status"`
	AuthMode     string               `json:"authMode,omitempty"`
	Plan         string               `json:"plan,omitempty"`
	Windows      []QuotaWindow        `json:"windows"`
	ReadAt       time.Time            `json:"readAt"`
	Stale        bool                 `json:"stale"`
	Message      string               `json:"message,omitempty"`
}
type QuotaProvider interface {
	AccountQuota(context.Context) (QuotaSnapshot, error)
}

func (r *Registry) AccountQuota(ctx context.Context, id ID) (QuotaSnapshot, error) {
	p, err := r.Lookup(id)
	if err != nil {
		return QuotaSnapshot{}, err
	}
	if q, ok := p.(QuotaProvider); ok {
		return q.AccountQuota(ctx)
	}
	return QuotaSnapshot{Status: "unsupported", Windows: []QuotaWindow{}, Message: "该 Agent 暂不提供订阅额度"}, nil
}

type quotaEntry struct {
	mu        sync.Mutex
	value     QuotaSnapshot
	attempted time.Time
}
type QuotaCache struct {
	mu      sync.Mutex
	entries map[ID]*quotaEntry
	read    func(context.Context, ID) (QuotaSnapshot, error)
	now     func() time.Time
}

func NewQuotaCache(read func(context.Context, ID) (QuotaSnapshot, error)) *QuotaCache {
	return &QuotaCache{entries: map[ID]*quotaEntry{}, read: read, now: time.Now}
}
func cloneQuota(q QuotaSnapshot) QuotaSnapshot {
	q.Windows = append([]QuotaWindow{}, q.Windows...)
	if q.ResetCredits != nil {
		summary := *q.ResetCredits
		if summary.Credits != nil {
			summary.Credits = append([]ResetCredit{}, summary.Credits...)
		}
		for i := range summary.Credits {
			if summary.Credits[i].ExpiresAt != nil {
				expiry := *summary.Credits[i].ExpiresAt
				summary.Credits[i].ExpiresAt = &expiry
			}
		}
		q.ResetCredits = &summary
	}
	return q
}
func (c *QuotaCache) Read(ctx context.Context, id ID, force bool) QuotaSnapshot {
	e := c.entry(id)
	e.mu.Lock()
	defer e.mu.Unlock()
	now := c.now()
	if !force && e.value.Status == "unsupported" && e.value.AuthMode == "api" {
		return cloneQuota(e.value)
	}
	ttl := 15 * time.Minute
	if force {
		ttl = 30 * time.Second
	}
	if !e.attempted.IsZero() && now.Sub(e.attempted) < ttl {
		return cloneQuota(e.value)
	}
	if ctx.Err() != nil {
		return QuotaSnapshot{Provider: id, Status: "error", Windows: []QuotaWindow{}, Message: "额度查询已取消"}
	}
	q, err := c.read(ctx, id)
	e.attempted = now
	if err != nil {
		q = QuotaSnapshot{Status: "error", Message: "额度查询失败，请稍后重试", Windows: []QuotaWindow{}}
		if e.value.Status == "available" && now.Sub(e.value.ReadAt) < time.Hour {
			q = cloneQuota(e.value)
			q.Stale = true
			q.Message = "刷新失败，显示上次读取的额度"
		}
		if e.value.Status == "unsupported" && e.value.AuthMode == "api" {
			q = cloneQuota(e.value)
			q.Stale = true
			q.Message = "刷新登录方式失败，自动额度查询仍暂停；请稍后手动刷新"
		}
	} else {
		q.ReadAt = c.now()
		if q.Status != "available" {
			q.Windows = []QuotaWindow{}
		}
	}
	q.Provider = id
	if q.Windows == nil {
		q.Windows = []QuotaWindow{}
	}
	e.value = cloneQuota(q)
	return cloneQuota(q)
}

func (c *QuotaCache) entry(id ID) *quotaEntry {
	c.mu.Lock()
	e := c.entries[id]
	if e == nil {
		e = &quotaEntry{}
		c.entries[id] = e
	}
	c.mu.Unlock()
	return e
}

// Serialize an explicit details read or mutation with ordinary reads. Bypass
// polling TTLs and replace the cache before another reader may use old values.
func (c *QuotaCache) Refresh(ctx context.Context, id ID, read func(context.Context) (QuotaSnapshot, error)) (QuotaSnapshot, error) {
	e := c.entry(id)
	e.mu.Lock()
	defer e.mu.Unlock()
	if ctx.Err() != nil {
		return QuotaSnapshot{}, ctx.Err()
	}
	q, err := read(ctx)
	if err != nil {
		e.attempted = time.Time{}
		e.value.Stale = true
		e.value.ResetCredits = nil
		e.value.Message = "状态可能已改变，请刷新额度"
		return cloneQuota(e.value), err
	}
	if q.Status == "" {
		return QuotaSnapshot{}, errors.New("missing quota status")
	}
	q.Provider = id
	q.ReadAt = c.now()
	q.Stale = false
	if q.Windows == nil {
		q.Windows = []QuotaWindow{}
	}
	e.value = cloneQuota(q)
	e.attempted = c.now()
	return cloneQuota(q), nil
}

// A missing count must never be displayed as a known zero balance.
func (s *ResetCreditsSummary) UnmarshalJSON(data []byte) error {
	var wire struct {
		AvailableCount *int64        `json:"availableCount"`
		Credits        []ResetCredit `json:"credits"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.AvailableCount == nil || *wire.AvailableCount < 0 {
		return errors.New("invalid reset credit count")
	}
	s.AvailableCount = *wire.AvailableCount
	s.Credits = wire.Credits
	return nil
}
