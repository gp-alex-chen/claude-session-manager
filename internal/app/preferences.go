package app

func (a *App) LoadPreferences() (map[string]string, error) {
	return a.store.Preferences()
}

func (a *App) SetPreference(key, value string) error {
	return a.store.SetPreference(key, value)
}
