package provider

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

type ResetCreditProvider interface {
	ReadResetCredits(context.Context) (QuotaSnapshot, error)
	RedeemResetCredit(context.Context, string, string, string, bool) (ResetCreditRedemption, error)
}
type ResetCreditView struct {
	Snapshot        QuotaSnapshot `json:"snapshot"`
	OfferToken      string        `json:"offerToken"`
	Pending         bool          `json:"pending"`
	PendingCreditID string        `json:"pendingCreditId"`
	Message         string        `json:"message"`
}
type ResetCreditRedemption struct {
	Outcome  string         `json:"outcome"`
	Snapshot *QuotaSnapshot `json:"snapshot,omitempty"`
	Message  string         `json:"message"`
}
type resetOffer struct {
	account       string
	created       time.Time
	summary       *ResetCreditsSummary
	key, creditID string
	sent          bool
	result        *ResetCreditRedemption
}

// Offers and uncertain attempts exist only in this application lifecycle.
// Never evict an uncertain attempt to permit a different logical redemption.
type ResetCreditService struct {
	mu       sync.Mutex
	provider ResetCreditProvider
	cache    *QuotaCache
	offers   map[string]*resetOffer
	now      func() time.Time
}

func NewResetCreditService(p ResetCreditProvider, cache *QuotaCache) *ResetCreditService {
	return &ResetCreditService{provider: p, cache: cache, offers: make(map[string]*resetOffer), now: time.Now}
}
func (s *ResetCreditService) Read(ctx context.Context) (ResetCreditView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, err := s.cache.Refresh(ctx, Codex, s.provider.ReadResetCredits)
	if err != nil {
		return ResetCreditView{}, errors.New("重置卡详情读取失败，请稍后刷新；较旧的 Codex CLI 可能不支持此功能")
	}
	view := ResetCreditView{Snapshot: q}
	for token, offer := range s.offers {
		if offer.account == q.AccountKey && offer.sent && offer.result == nil {
			view.OfferToken = token
			view.Pending = true
			view.PendingCreditID = offer.creditID
			view.Message = "上次使用结果未知，请核对原请求；不会另外消耗一张卡。"
			return view, nil
		}
	}
	if q.Status != "available" || q.ResetCredits == nil {
		view.Message = "当前账户或 CLI 未提供重置卡信息"
		return view, nil
	}
	if q.AccountKey == "" {
		view.Message = "当前 CLI 未提供可核对的账户标识，仅展示卡片，暂不可使用"
		return view, nil
	}
	if q.ResetCredits.AvailableCount <= 0 {
		return view, nil
	}
	// Bound idle/completed offers. Invalidated tokens cannot trigger a new attempt.
	for token, offer := range s.offers {
		if !(offer.sent && offer.result == nil) && s.now().Sub(offer.created) > 5*time.Minute {
			delete(s.offers, token)
		}
	}
	if len(s.offers) >= 128 {
		view.Message = "请稍后重新打开详情"
		return view, nil
	}
	token := uuid.NewString()
	s.offers[token] = &resetOffer{account: q.AccountKey, created: s.now(), summary: cloneQuota(q).ResetCredits}
	view.OfferToken = token
	return view, nil
}
func validResetCredit(summary *ResetCreditsSummary, id string, now time.Time) bool {
	if summary == nil || summary.AvailableCount <= 0 {
		return false
	}
	if id == "" {
		return len(summary.Credits) == 0
	}
	for _, credit := range summary.Credits {
		if credit.ID == id {
			return credit.Status == "available" && credit.ResetType == "codexRateLimits" && (credit.ExpiresAt == nil || *credit.ExpiresAt > now.Unix())
		}
	}
	return false
}
func cloneRedemption(r ResetCreditRedemption) ResetCreditRedemption {
	if r.Snapshot != nil {
		q := cloneQuota(*r.Snapshot)
		r.Snapshot = &q
	}
	return r
}
func (s *ResetCreditService) Consume(ctx context.Context, token, creditID string) (ResetCreditRedemption, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	offer := s.offers[token]
	if offer == nil || offer.account == "" {
		return ResetCreditRedemption{}, errors.New("卡片信息已失效，请重新打开详情")
	}
	if offer.key != "" && offer.creditID != creditID {
		return ResetCreditRedemption{}, errors.New("不能更换同一请求中的卡片")
	}
	if offer.result != nil {
		// Cache only the definite outcome. The original usage snapshot may belong
		// to an earlier read/account by the time an IPC retry arrives.
		result := cloneRedemption(*offer.result)
		result.Snapshot = nil
		current, err := s.cache.Refresh(ctx, Codex, s.provider.ReadResetCredits)
		if err == nil && current.AccountKey == offer.account {
			result.Snapshot = &current
		} else {
			result.Message += " 最新额度未确认，请重新打开详情。"
		}
		return result, nil
	}
	if !offer.sent {
		if s.now().Sub(offer.created) > 5*time.Minute || !validResetCredit(offer.summary, creditID, s.now()) {
			return ResetCreditRedemption{}, errors.New("这张重置卡当前不可使用，请刷新详情")
		}
		for _, other := range s.offers {
			if other != offer && other.account == offer.account && other.sent && other.result == nil {
				return ResetCreditRedemption{}, errors.New("请先核对上次使用结果")
			}
		}
		if offer.key == "" {
			offer.key = uuid.NewString()
			offer.creditID = creditID
		}
	}
	var result ResetCreditRedemption
	_, err := s.cache.Refresh(ctx, Codex, func(ctx context.Context) (QuotaSnapshot, error) {
		var err error
		result, err = s.provider.RedeemResetCredit(ctx, offer.account, offer.creditID, offer.key, offer.sent)
		if err != nil {
			return QuotaSnapshot{}, err
		}
		offer.sent = true
		if result.Outcome != "unknown" {
			copy := cloneRedemption(result)
			copy.Snapshot = nil
			offer.result = &copy
		}
		if result.Snapshot == nil {
			return QuotaSnapshot{}, errors.New("最新额度尚未确认")
		}
		return *result.Snapshot, nil
	})
	if result.Outcome == "" {
		if err != nil {
			return result, err
		}
		return result, errors.New("未取得使用结果")
	}
	return cloneRedemption(result), nil
}
