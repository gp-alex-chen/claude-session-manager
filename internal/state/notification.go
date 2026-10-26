package state

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

type NotificationConfig struct {
	Enabled        bool     `json:"enabled"`
	Program        string   `json:"program"`
	Args           []string `json:"args"`
	WorkingDir     string   `json:"workingDir"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
	Events         []string `json:"events"`
}

func DefaultNotificationConfig() NotificationConfig {
	return NotificationConfig{Args: []string{}, TimeoutSeconds: 10,
		Events: []string{"input_requested", "permission_requested", "task_completed"}}
}

func NotificationEventValid(event string) bool {
	return event == "input_requested" || event == "permission_requested" || event == "task_completed" || event == "attention_changed"
}

func NormalizeNotificationConfig(c NotificationConfig) (NotificationConfig, error) {
	c.Program = strings.TrimSpace(c.Program)
	c.WorkingDir = strings.TrimSpace(c.WorkingDir)
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 120 {
		return c, errors.New("通知执行超时须为 1–120 秒")
	}
	if c.Enabled && c.Program == "" {
		return c, errors.New("请填写通知程序路径")
	}
	if len(c.Args) > 128 || len(c.Events) > 4 {
		return c, errors.New("通知参数或事件数量过多")
	}
	if c.WorkingDir != "" && !filepath.IsAbs(c.WorkingDir) {
		return c, errors.New("工作目录须为绝对路径")
	}
	if c.Args == nil {
		c.Args = []string{}
	}
	if c.Events == nil {
		c.Events = []string{}
	}
	seen := map[string]bool{}
	for _, e := range c.Events {
		if !NotificationEventValid(e) || seen[e] {
			return c, errors.New("通知事件无效或重复")
		}
		seen[e] = true
	}
	if c.Enabled && len(c.Events) == 0 {
		return c, errors.New("请至少选择一种通知事件")
	}
	values := append([]string{c.Program, c.WorkingDir}, c.Args...)
	total := 0
	for _, v := range values {
		total += len(v)
		if strings.ContainsRune(v, 0) || len(v) > 8192 {
			return c, errors.New("通知程序参数过长或包含无效字符")
		}
	}
	if total > 32<<10 {
		return c, errors.New("通知程序配置过长")
	}
	return c, nil
}

func (s *Store) NotificationConfig() (NotificationConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notificationConfigLocked()
}

func (s *Store) notificationConfigLocked() (NotificationConfig, error) {
	c := DefaultNotificationConfig()
	_, err := loadJSON(filepath.Join(s.dir, "notification.json"), &c)
	if err != nil {
		return c, err
	}
	return NormalizeNotificationConfig(c)
}

func (s *Store) SaveNotificationConfig(c NotificationConfig) error {
	c, err := NormalizeNotificationConfig(c)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withDocumentLock(".notification.lock", func() error {
		if _, err := s.notificationConfigLocked(); err != nil {
			return err
		}
		data, err := json.Marshal(c)
		if err != nil {
			return err
		}
		return atomicWrite(filepath.Join(s.dir, "notification.json"), data)
	})
}
