package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

type codexQuotaBucket struct {
	Name      string            `json:"limitName"`
	Plan      string            `json:"planType"`
	Primary   *codexQuotaWindow `json:"primary"`
	Secondary *codexQuotaWindow `json:"secondary"`
}
type codexQuotaWindow struct {
	Used    *float64 `json:"usedPercent"`
	Minutes *int64   `json:"windowDurationMins"`
	Reset   *int64   `json:"resetsAt"`
}

func (c *CodexRPC) AccountQuota(parent context.Context) (QuotaSnapshot, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	conn, err := c.Open(ctx)
	if err != nil {
		return QuotaSnapshot{}, err
	}
	defer conn.Close()
	r := newRPCSession(ctx, conn)
	var init map[string]any
	if err = r.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "claude_codex_session_manager", "version": "1"}}, &init); err != nil {
		return QuotaSnapshot{}, err
	}
	if _, err = conn.Write([]byte("{\"method\":\"initialized\"}\n")); err != nil {
		return QuotaSnapshot{}, err
	}
	var responseAccount struct {
		Account json.RawMessage `json:"account"`
	}
	if err = r.call("account/read", map[string]bool{"refreshToken": false}, &responseAccount); err != nil {
		return QuotaSnapshot{}, err
	}
	if len(responseAccount.Account) == 0 {
		return QuotaSnapshot{}, errors.New("Codex 未返回账户状态")
	}
	var account *struct {
		Type     string
		PlanType string `json:"planType"`
	}
	if err = json.Unmarshal(responseAccount.Account, &account); err != nil {
		return QuotaSnapshot{}, err
	}
	q := QuotaSnapshot{Status: "available", Windows: []QuotaWindow{}}
	if account == nil {
		q.Status = "signed_out"
		q.Message = "Codex 尚未登录"
		return q, nil
	}
	if account.Type != "chatgpt" {
		q.Status = "unsupported"
		q.Message = "当前登录方式不提供订阅额度"
		if account.Type == "apiKey" {
			q.AuthMode = "api"
			q.Message = "当前使用 API 方式，自动额度查询已暂停；切换订阅登录后请手动刷新"
		}
		return q, nil
	}
	q.AuthMode = "subscription"
	q.Plan = account.PlanType
	var response struct {
		Legacy  *codexQuotaBucket            `json:"rateLimits"`
		Buckets map[string]*codexQuotaBucket `json:"rateLimitsByLimitId"`
	}
	if err = r.call("account/rateLimits/read", map[string]any{"excludeResetCreditDetails": true}, &response); err != nil {
		return q, err
	}
	buckets := response.Buckets
	if len(buckets) == 0 && response.Legacy != nil {
		buckets = map[string]*codexQuotaBucket{"codex": response.Legacy}
	}
	if buckets["codex"] == nil && response.Legacy != nil {
		buckets["codex"] = response.Legacy
	}
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		bucket := buckets[key]
		if bucket == nil {
			continue
		}
		for i, w := range []*codexQuotaWindow{bucket.Primary, bucket.Secondary} {
			if w == nil || w.Used == nil || w.Minutes == nil {
				continue
			}
			if math.IsNaN(*w.Used) || math.IsInf(*w.Used, 0) || *w.Used < 0 || *w.Used > 100 || *w.Minutes <= 0 {
				return q, errors.New("无效的 Codex 额度响应")
			}
			label := fmt.Sprintf("%d 分钟", *w.Minutes)
			if *w.Minutes == 300 {
				label = "5 小时"
			} else if *w.Minutes == 10080 {
				label = "周"
			}
			scope := ""
			if key != "codex" {
				scope = bucket.Name
				if scope == "" {
					scope = key
				}
			}
			row := QuotaWindow{ID: fmt.Sprintf("%s:%d", key, i), Label: label, UsedPercent: *w.Used, WindowMinutes: *w.Minutes, Scope: scope}
			if scope != "" {
				row.Label = scope + " · " + label
			}
			if w.Reset != nil {
				row.ResetsAt = *w.Reset
			}
			q.Windows = append(q.Windows, row)
		}
	}
	if len(q.Windows) == 0 {
		return q, errors.New("Codex 未返回可读取的订阅额度")
	}
	return q, nil
}
