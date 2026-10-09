package app

// SetCompletionBadge is fed by the serialized frontend completion write queue.
func (a *App) SetCompletionBadge(count int, force bool) error {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.completionBadge == nil {
		return nil
	}
	return a.completionBadge.Set(count, force)
}
