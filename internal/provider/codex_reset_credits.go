package provider

import (
	"context"
	"errors"
	"time"
)

func (c *CodexRPC) ReadResetCredits(ctx context.Context) (QuotaSnapshot, error) {
	return c.accountQuota(ctx, true)
}
func (c *CodexRPC) RedeemResetCredit(parent context.Context, accountKey, creditID, key string, retry bool) (ResetCreditRedemption, error) {
	if accountKey == "" || key == "" {
		return ResetCreditRedemption{}, errors.New("无效的重置卡请求")
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	conn, err := c.Open(ctx)
	if err != nil {
		return ResetCreditRedemption{}, errors.New("无法连接 Codex，请重新打开详情")
	}
	defer conn.Close()
	rpc := newRPCSession(ctx, conn)
	var init map[string]any
	if err = rpc.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "claude_codex_session_manager", "version": "1"}}, &init); err != nil {
		return ResetCreditRedemption{}, errors.New("Codex 初始化失败")
	}
	if _, err = conn.Write([]byte("{\"method\":\"initialized\"}\n")); err != nil {
		return ResetCreditRedemption{}, errors.New("Codex 初始化失败")
	}
	before, err := c.readAccountQuota(rpc, true)
	if err != nil {
		return ResetCreditRedemption{}, errors.New("使用前读取账户失败，请刷新详情")
	}
	if before.Status != "available" || before.AccountKey == "" || before.AccountKey != accountKey {
		return ResetCreditRedemption{}, errors.New("Codex 账户已变化，请重新打开详情后确认")
	}
	if !retry && !validResetCredit(before.ResetCredits, creditID, time.Now()) {
		return ResetCreditRedemption{}, errors.New("所选重置卡已不可用，请刷新详情")
	}
	if ctx.Err() != nil {
		return ResetCreditRedemption{}, ctx.Err()
	}
	params := map[string]any{"idempotencyKey": key}
	if creditID != "" {
		params["creditId"] = creditID
	}
	var response struct {
		Outcome string `json:"outcome"`
	}
	// No automatic retry: a failed write/response may already have consumed the card.
	if err = rpc.call("account/rateLimitResetCredit/consume", params, &response); err != nil {
		return ResetCreditRedemption{Outcome: "unknown", Message: "使用结果未知，请核对上次请求；不会自动重试。"}, nil
	}
	result := ResetCreditRedemption{Outcome: response.Outcome}
	switch response.Outcome {
	case "reset":
		result.Message = "重置卡已使用，额度已重置。"
	case "alreadyRedeemed":
		result.Message = "已确认上次重置成功，没有重复消耗卡片。"
	case "nothingToReset":
		result.Message = "当前没有可重置的额度窗口。"
	case "noCredit":
		result.Message = "当前没有可用重置卡。"
	default:
		return ResetCreditRedemption{Outcome: "unknown", Message: "Codex 返回了未知结果，请核对上次请求。"}, nil
	}
	after, err := c.readAccountQuota(rpc, true)
	if err == nil && after.AccountKey == accountKey {
		after.Provider = Codex
		result.Snapshot = &after
	} else {
		result.Message += " 最新额度读取失败，请手动刷新；不要重复使用。"
	}
	return result, nil
}
