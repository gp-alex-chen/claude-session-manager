package app

import (
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/provider"
)

// Include the reference query and subsequent provider/terminal persistence.
const agentStopWaitTimeout = provider.AntigravityRestoreTimeout + 15*time.Second

// A stopping flag is an ownership claim, not proof that the restore snapshot
// has reached disk. Window shutdown must await an in-flight update's owner.
func (a *App) claimStop() (uint64, chan struct{}, bool) {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.lifecycleID != 0 && a.ctx == nil {
		return a.lifecycleID, nil, false
	}
	if a.stopping {
		return a.lifecycleID, a.stoppingDone, false
	}
	a.stopping = true
	a.stoppingDone = make(chan struct{})
	return a.lifecycleID, a.stoppingDone, true
}

func (a *App) claimShutdownStop() (uint64, chan struct{}, bool) {
	for {
		generation, done, owner := a.claimStop()
		if owner || done == nil {
			return generation, done, owner
		}
		select {
		case <-done:
		case <-time.After(agentStopWaitTimeout):
			a.log("等待已有退出保存流程超时")
			return generation, nil, false
		}
		a.lifecycleMu.Lock()
		same := a.lifecycleID == generation
		a.lifecycleMu.Unlock()
		if !same {
			return generation, nil, false
		}
	}
}

func (a *App) completeStop(generation uint64, done chan struct{}, reopen bool) {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if reopen && a.lifecycleID == generation && a.stoppingDone == done {
		a.stopping = false
		a.stoppingDone = nil
	}
	close(done)
}
