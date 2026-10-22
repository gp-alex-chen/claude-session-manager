package app

// SetCompletionBadge preserves the count-only binding for older frontends.
func (a *App) SetCompletionBadge(count int, force bool) error {
	return a.SetTaskbarStatus(count, false, force)
}

// SetTaskbarStatus applies completion and waiting state as one atomic snapshot.
func (a *App) SetTaskbarStatus(count int, attention, force bool) error {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.completionBadge == nil {
		return nil
	}
	return a.completionBadge.SetStatus(count, attention, force)
}
