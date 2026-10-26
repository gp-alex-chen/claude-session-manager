package notify

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/agent"
)

type ProgramEvent struct {
	SchemaVersion int             `json:"schema_version"`
	EventID       string          `json:"event_id"`
	Event         string          `json:"event"`
	OccurredAt    string          `json:"occurred_at"`
	Provider      string          `json:"provider"`
	SessionID     string          `json:"session_id"`
	RuntimeToken  string          `json:"runtime_token,omitempty"`
	SessionName   string          `json:"session_name"`
	ProjectPath   string          `json:"project_path"`
	State         string          `json:"state"`
	WaitingFor    string          `json:"waiting_for,omitempty"`
	TurnID        string          `json:"turn_id,omitempty"`
	Test          bool            `json:"test"`
	Attention     *AttentionState `json:"attention,omitempty"`
	Force         bool            `json:"force,omitempty"`
	Reason        string          `json:"reason,omitempty"`
}

func newProgramEvent(kind, provider string, a agent.AgentInfo) ProgramEvent {
	var id [16]byte
	_, _ = rand.Read(id[:])
	return ProgramEvent{SchemaVersion: 1, EventID: hex.EncodeToString(id[:]), Event: kind,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano), Provider: provider,
		SessionID: a.SessionID, RuntimeToken: a.RuntimeToken, SessionName: a.Name,
		ProjectPath: a.Cwd, State: a.State, WaitingFor: a.WaitingFor, TurnID: a.TurnID}
}

type eventObservation struct {
	agent         agent.AgentInfo
	completedTurn string
	waitingKind   string
}
type eventDetector struct {
	sources map[string]map[string]eventObservation
}

func newEventDetector() *eventDetector {
	return &eventDetector{sources: map[string]map[string]eventObservation{}}
}
func busy(a agent.AgentInfo) bool {
	return a.State == "working" || a.State == "queued" || a.Status == "busy"
}
func waiting(a agent.AgentInfo) string {
	if busy(a) || (a.State != "blocked" && a.Status != "waiting") {
		return ""
	}
	if a.WaitingFor == "userInput" {
		return "input_requested"
	}
	if a.WaitingFor == "approval" {
		return "permission_requested"
	}
	return ""
}
func unsuccessful(a agent.AgentInfo) bool {
	return a.State == "blocked" || a.Status == "waiting" || a.State == "failed" || a.State == "interrupted" || a.State == "unknown" || a.TurnStatus == "failed" || a.TurnStatus == "interrupted"
}

// Only successful source snapshots reach this detector. Absence is an exit,
// never proof that a task completed. Each source has its own startup baseline.
func (d *eventDetector) observe(provider string, list []agent.AgentInfo) []ProgramEvent {
	previous, ready := d.sources[provider]
	next := map[string]eventObservation{}
	var events []ProgramEvent
	for _, a := range list {
		key := a.RuntimeToken
		if key == "" {
			key = a.SessionID
		}
		if key == "" {
			continue
		}
		old, existed := previous[key]
		current := eventObservation{agent: a, completedTurn: old.completedTurn, waitingKind: old.waitingKind}
		kind := waiting(a)
		if ready && kind != "" && (!existed || old.waitingKind != kind) {
			events = append(events, newProgramEvent(kind, provider, a))
		}
		if kind != "" {
			current.waitingKind = kind
		} else if a.State != "unknown" && a.State != "blocked" && a.Status != "waiting" {
			current.waitingKind = ""
		}
		if provider == "codex" {
			if a.TurnID != "" && a.TurnStatus == "completed" && !busy(a) && kind == "" && !unsuccessful(a) {
				if ready && existed && old.completedTurn != a.TurnID {
					events = append(events, newProgramEvent("task_completed", provider, a))
				}
				current.completedTurn = a.TurnID
			}
		} else if ready && existed && !busy(a) && kind == "" && !unsuccessful(a) &&
			((a.State == "done" && old.agent.State != "done") || (busy(old.agent) && (a.State == "idle" || a.Status == "idle"))) {
			events = append(events, newProgramEvent("task_completed", provider, a))
		}
		next[key] = current
	}
	d.sources[provider] = next
	return events
}
