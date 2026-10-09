// Package provider defines the agent-specific boundary shared by CLI adapters.
package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/gp-alex-chen/claude-session-manager/internal/agent"
	"github.com/gp-alex-chen/claude-session-manager/internal/session"
	"github.com/gp-alex-chen/claude-session-manager/internal/usage"
)

type ID string

const (
	Claude ID = "claude"
	Codex  ID = "codex"
)

type SessionRef struct {
	Provider ID
	ID       string
}

// Legacy Claude keys stay unchanged. ':' is not valid in a Claude raw ID,
// so namespaced keys cannot collide with existing application state.
func EncodeKey(ref SessionRef) (string, error) {
	if err := validateRef(ref); err != nil {
		return "", err
	}
	if ref.Provider == Claude {
		return ref.ID, nil
	}
	return string(ref.Provider) + ":" + url.PathEscape(ref.ID), nil
}

func DecodeKey(key string) (SessionRef, error) {
	ref := SessionRef{Claude, key}
	if p, id, ok := strings.Cut(key, ":"); ok {
		raw, err := url.PathUnescape(id)
		if err != nil {
			return SessionRef{}, err
		}
		ref = SessionRef{ID(p), raw}
	}
	if err := validateRef(ref); err != nil {
		return ref, err
	}
	canonical, err := EncodeKey(ref)
	if err != nil || canonical != key {
		return ref, errors.New("non-canonical session key")
	}
	return ref, nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var providerPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func validateRef(ref SessionRef) error {
	switch ref.Provider {
	case Claude:
		return session.ValidateID(ref.ID)
	case Codex:
		if uuidPattern.MatchString(ref.ID) {
			return nil
		}
		return errors.New("invalid Codex session UUID")
	default:
		if !providerPattern.MatchString(string(ref.Provider)) || ref.ID == "" || len(ref.ID) > 256 || strings.ContainsAny(ref.ID, "\x00\r\n") {
			return errors.New("invalid session reference")
		}
		return nil
	}
}

type Descriptor struct {
	ID                   ID     `json:"id"`
	Name                 string `json:"name"`
	HasHistory           bool   `json:"hasHistory"`
	HasStatus            bool   `json:"hasStatus"`
	HasUsage             bool   `json:"hasUsage"`
	HasRename            bool   `json:"hasRename"`
	NewSessionRestorable bool   `json:"newSessionRestorable"`
}

type Session struct {
	Ref       SessionRef
	Dir, Name string
	Time      time.Time
}
type OpenRequest struct {
	Ref          *SessionRef
	Dir          string
	RuntimeToken string
}
type LaunchSpec struct {
	Command  string
	Args     []string
	Identity *SessionRef
	Env      []string
	Close    func()
}

type AgentProvider interface {
	Descriptor() Descriptor
	ValidateSessionID(string) error
}
type TerminalProvider interface {
	PrepareLaunch(context.Context, OpenRequest) (LaunchSpec, error)
}
type HistoryProvider interface {
	ListSessions(context.Context) ([]Session, error)
}
type StatusSource interface {
	Status(context.Context) ([]agent.AgentInfo, error)
}

// RestorableSource confirms native persistence separately from ID allocation.
// Returned IDs are provider-local; force bypasses a cached negative result.
type RestorableSource interface {
	RestorableSessions(context.Context, bool) ([]string, error)
}

// RestorationSealer fixes the final identity snapshot before terminal teardown.
type RestorationSealer interface{ SealRestoration() }
type UsageProvider interface {
	Usage(context.Context, string, string) (usage.Summary, error)
}

type RenameProvider interface {
	Rename(context.Context, string, string) error
}

func (r *Registry) Rename(ctx context.Context, ref SessionRef, name string) error {
	var err error
	name, err = ValidateTitle(name)
	if err != nil {
		return err
	}
	if err := validateRef(ref); err != nil {
		return err
	}
	p, err := r.Lookup(ref.Provider)
	if err != nil {
		return err
	}
	if err := p.ValidateSessionID(ref.ID); err != nil {
		return err
	}
	renamer, ok := p.(RenameProvider)
	if !ok {
		return fmt.Errorf("%s 暂不支持原生重命名", p.Descriptor().Name)
	}
	return renamer.Rename(ctx, ref.ID, name)
}

func ValidateTitle(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("会话名称不能为空")
	}
	if len([]rune(name)) > 120 {
		return "", errors.New("会话名称最多 120 个字符")
	}
	for _, char := range name {
		if unicode.IsControl(char) {
			return "", errors.New("会话名称不能包含控制字符")
		}
	}
	return name, nil
}

type Registry struct{ providers []AgentProvider }

func NewRegistry() *Registry { return &Registry{} }
func (r *Registry) Register(p AgentProvider) error {
	if p == nil {
		return errors.New("nil agent provider")
	}
	v := reflect.ValueOf(p)
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
		return errors.New("nil agent provider")
	}
	id := p.Descriptor().ID
	if !providerPattern.MatchString(string(id)) {
		return errors.New("empty agent provider ID")
	}
	for _, existing := range r.providers {
		if existing.Descriptor().ID == id {
			return fmt.Errorf("duplicate agent %q", id)
		}
	}
	if _, ok := p.(TerminalProvider); !ok {
		return errors.New("agent has no terminal adapter")
	}
	r.providers = append(r.providers, p)
	return nil
}
func (r *Registry) Lookup(id ID) (AgentProvider, error) {
	for _, p := range r.providers {
		if p.Descriptor().ID == id {
			return p, nil
		}
	}
	return nil, fmt.Errorf("unknown agent %q", id)
}
func (r *Registry) Descriptors() []Descriptor {
	out := make([]Descriptor, 0, len(r.providers))
	for _, p := range r.providers {
		d := p.Descriptor()
		_, d.HasHistory = p.(HistoryProvider)
		_, d.HasStatus = p.(StatusSource)
		_, d.HasUsage = p.(UsageProvider)
		_, d.HasRename = p.(RenameProvider)
		out = append(out, d)
	}
	return out
}

func (r *Registry) PrepareLaunch(ctx context.Context, id ID, req OpenRequest) (LaunchSpec, error) {
	p, err := r.Lookup(id)
	if err != nil {
		return LaunchSpec{}, err
	}
	if req.Ref != nil {
		if req.Ref.Provider != id {
			return LaunchSpec{}, errors.New("session belongs to another agent")
		}
		if err := p.ValidateSessionID(req.Ref.ID); err != nil {
			return LaunchSpec{}, err
		}
	}
	return p.(TerminalProvider).PrepareLaunch(ctx, req)
}

func (r *Registry) Sessions(ctx context.Context) ([]Session, []error) {
	return r.SessionsFor(ctx, nil)
}

// SessionsFor skips unrequested adapters before doing any history I/O.
// nil preserves the all-provider query; an empty non-nil slice reads none.
func (r *Registry) SessionsFor(ctx context.Context, ids []ID) ([]Session, []error) {
	list, _, warnings := r.QuerySessions(ctx, ids)
	return list, warnings
}

func (r *Registry) QuerySessions(ctx context.Context, ids []ID) ([]Session, []ID, []error) {
	selected := make(map[ID]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	var out []Session
	var warnings []error
	var succeeded []ID
	for _, p := range r.providers {
		if ids != nil && !selected[p.Descriptor().ID] {
			continue
		}
		h, ok := p.(HistoryProvider)
		if !ok {
			continue
		}
		list, err := h.ListSessions(ctx)
		if err != nil {
			warnings = append(warnings, fmt.Errorf("%s history: %w", p.Descriptor().Name, err))
		} else {
			succeeded = append(succeeded, p.Descriptor().ID)
		}
		for _, s := range list {
			if s.Ref.Provider != p.Descriptor().ID || p.ValidateSessionID(s.Ref.ID) != nil {
				warnings = append(warnings, fmt.Errorf("%s returned an invalid session", p.Descriptor().Name))
				continue
			}
			out = append(out, s)
		}
	}
	return out, succeeded, warnings
}

func (r *Registry) Status(ctx context.Context) ([]agent.AgentInfo, []error) {
	return r.StatusExcept(ctx, "")
}

func (r *Registry) StatusExcept(ctx context.Context, excluded ID) ([]agent.AgentInfo, []error) {
	var out []agent.AgentInfo
	var warnings []error
	for _, p := range r.providers {
		if p.Descriptor().ID == excluded {
			continue
		}
		s, ok := p.(StatusSource)
		if !ok {
			continue
		}
		list, err := s.Status(ctx)
		if err != nil {
			warnings = append(warnings, err)
			continue
		}
		for _, status := range list {
			key, err := EncodeKey(SessionRef{p.Descriptor().ID, status.SessionID})
			if err != nil {
				continue
			}
			status.SessionID = key
			status.Provider = string(p.Descriptor().ID)
			out = append(out, status)
		}
	}
	return out, warnings
}

func (r *Registry) Usage(ctx context.Context, ref SessionRef, dir string) usage.Summary {
	p, err := r.Lookup(ref.Provider)
	if err != nil {
		return usage.Summary{Warnings: []string{err.Error()}}
	}
	u, ok := p.(UsageProvider)
	if !ok {
		return usage.Summary{Warnings: []string{p.Descriptor().Name + " 用量暂不支持"}}
	}
	result, err := u.Usage(ctx, ref.ID, dir)
	if err != nil {
		return usage.Summary{Warnings: []string{err.Error()}}
	}
	return result
}
