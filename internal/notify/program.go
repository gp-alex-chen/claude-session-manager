package notify

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/gp-alex-chen/claude-session-manager/internal/agent"
	"github.com/gp-alex-chen/claude-session-manager/internal/state"
)

const programQueueLimit = 32

type ProgramResult struct {
	EventID    string `json:"eventId"`
	Event      string `json:"event"`
	Test       bool   `json:"test"`
	Success    bool   `json:"success"`
	ExitCode   int    `json:"exitCode"`
	Error      string `json:"error"`
	FinishedAt string `json:"finishedAt"`
}
type ProgramStatus struct {
	Running bool           `json:"running"`
	Pending int            `json:"pending"`
	Dropped int            `json:"dropped"`
	Last    *ProgramResult `json:"last"`
}
type programJob struct {
	event      ProgramEvent
	config     state.NotificationConfig
	generation uint64
}
type programRunner func(context.Context, state.NotificationConfig, []byte) ProgramResult
type ProgramService struct {
	mu           sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	wake         chan struct{}
	config       state.NotificationConfig
	detector     *eventDetector
	queue        []programJob
	generation   uint64
	activeCancel context.CancelFunc
	closed       bool
	status       ProgramStatus
	runner       programRunner
	onResult     func(ProgramResult)
}

func NewProgramService(ctx context.Context, cfg state.NotificationConfig) *ProgramService {
	return newProgramService(ctx, cfg, runProgram, nil)
}
func newProgramService(ctx context.Context, cfg state.NotificationConfig, run programRunner, onResult func(ProgramResult)) *ProgramService {
	ctx, cancel := context.WithCancel(ctx)
	s := &ProgramService{ctx: ctx, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1),
		config: cloneProgramConfig(cfg), detector: newEventDetector(), runner: run, onResult: onResult}
	go s.work()
	return s
}
func cloneProgramConfig(c state.NotificationConfig) state.NotificationConfig {
	c.Args = append([]string{}, c.Args...)
	c.Events = append([]string{}, c.Events...)
	return c
}
func (s *ProgramService) Configure(c state.NotificationConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if reflect.DeepEqual(s.config, c) {
		return
	}
	s.generation++
	s.config = cloneProgramConfig(c)
	s.queue = nil
	s.status.Pending = 0
	if s.activeCancel != nil {
		s.activeCancel()
	}
}
func (s *ProgramService) Status() ProgramStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.status
	if out.Last != nil {
		last := *out.Last
		out.Last = &last
	}
	return out
}
func (s *ProgramService) Observe(provider string, list []agent.AgentInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return
	}
	for _, e := range s.detector.observe(provider, list) {
		if !s.config.Enabled {
			continue
		}
		for _, kind := range s.config.Events {
			if kind == e.Event {
				_ = s.enqueueLocked(e)
				break
			}
		}
	}
}
func (s *ProgramService) Test(kind string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !state.NotificationEventValid(kind) {
		return errors.New("无效的测试事件")
	}
	if s.config.Program == "" {
		return errors.New("请先保存通知程序配置")
	}
	e := newProgramEvent(kind, "codex", agent.AgentInfo{SessionID: "test-session", RuntimeToken: "test-runtime",
		Name: "TheOne 测试通知", State: "blocked", WaitingFor: "approval"})
	if kind == "input_requested" {
		e.WaitingFor = "userInput"
	}
	if kind == "task_completed" {
		e.State = "done"
		e.WaitingFor = ""
	}
	e.Test = true
	return s.enqueueLocked(e)
}
func (s *ProgramService) enqueueLocked(e ProgramEvent) error {
	if s.closed || s.ctx.Err() != nil {
		return errors.New("通知服务已停止")
	}
	if len(s.queue) >= programQueueLimit {
		s.status.Dropped++
		return errors.New("通知队列已满，本次事件未发送")
	}
	s.queue = append(s.queue, programJob{e, cloneProgramConfig(s.config), s.generation})
	s.status.Pending = len(s.queue)
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}
func (s *ProgramService) work() {
	defer close(s.done)
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		}
		for {
			s.mu.Lock()
			if s.closed || s.ctx.Err() != nil || len(s.queue) == 0 {
				s.mu.Unlock()
				break
			}
			job := s.queue[0]
			s.queue = s.queue[1:]
			s.status.Pending = len(s.queue)
			s.status.Running = true
			ctx, cancel := context.WithTimeout(s.ctx, time.Duration(job.config.TimeoutSeconds)*time.Second)
			s.activeCancel = cancel
			s.mu.Unlock()
			payload, _ := json.Marshal(job.event)
			result := s.runner(ctx, job.config, append(payload, '\n'))
			cancel()
			result.EventID = job.event.EventID
			result.Event = job.event.Event
			result.Test = job.event.Test
			result.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			s.mu.Lock()
			s.activeCancel = nil
			s.status.Running = false
			if job.generation == s.generation {
				s.status.Last = &result
			}
			s.mu.Unlock()
			if s.onResult != nil {
				s.onResult(result)
			}
		}
	}
}
func (s *ProgramService) Stop() {
	s.mu.Lock()
	s.closed = true
	s.queue = nil
	s.status.Pending = 0
	s.cancel()
	if s.activeCancel != nil {
		s.activeCancel()
	}
	s.mu.Unlock()
}
func (s *ProgramService) Close() {
	s.Stop()
	<-s.done
}
