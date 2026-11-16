package provider

import (
	"context"
	"errors"
	"strings"
	"time"
)

// AntigravityRestoreTimeout bounds the whole exit query, including a queued
// lookup (15s), a fresh transport (15s), and process cleanup. Multiple pinned
// executables still share this one overall bound.
const AntigravityRestoreTimeout = 35 * time.Second

// RestoreReferences saves the most recently confirmed native reference only
// at a normal application exit. It does not promote live status observations
// into continuously persisted identities or promise recovery after a crash.
func (p *AntigravityAdapter) RestoreReferences(parent context.Context) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(parent, AntigravityRestoreTimeout)
	defer cancel()
	_, _ = p.Status(ctx)
	type candidate struct {
		runtime      *antigravityRuntime
		id, dir, bin string
	}
	candidates := map[string]candidate{}
	p.mu.Lock()
	for token, r := range p.runtimes {
		if r.identitySeen && !r.ambiguous && uuidPattern.MatchString(r.expectedID) {
			candidates[token] = candidate{r, r.expectedID, r.dir, r.bin}
		}
	}
	p.mu.Unlock()
	catalogs := map[string][]Session{}
	for _, c := range candidates {
		if _, ok := catalogs[c.bin]; ok {
			continue
		}
		list, err := p.readHistoryWithin(ctx, true, c.bin)
		if err != nil {
			return nil, err
		}
		catalogs[c.bin] = list
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, _ = p.Status(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	refs := map[string]string{}
	var failures []error
	for token, c := range candidates {
		r := p.runtimes[token]
		if r != c.runtime || !r.identitySeen || r.ambiguous || r.expectedID != c.id {
			continue
		}
		dir, err := normalizeAntigravityDirectory(c.dir)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, s := range catalogs[c.bin] {
			if s.Ref.Provider == Antigravity && s.Ref.ID == c.id && strings.EqualFold(s.Dir, dir) {
				refs[token], _ = EncodeKey(s.Ref)
				break
			}
		}
		if refs[token] == "" {
			failures = append(failures, errors.New("Antigravity 会话尚未在原生历史中保存或目录不一致，未加入恢复清单"))
		}
	}
	return refs, errors.Join(failures...)
}

// Called again immediately before the terminal manager's atomic close. No
// server is started here, and a closed/replaced/ambiguous runtime is excluded.
func (p *AntigravityAdapter) FilterRestoreReferences(refs map[string]string) map[string]string {
	_, _ = p.Status(context.Background())
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]string{}
	for token, key := range refs {
		r := p.runtimes[token]
		if r == nil || !r.identitySeen || r.ambiguous {
			continue
		}
		want, _ := EncodeKey(SessionRef{Antigravity, r.expectedID})
		if key == want {
			out[token] = key
		}
	}
	return out
}
