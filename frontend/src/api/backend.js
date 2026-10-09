// Single boundary for Wails-generated bindings. Keeping this import isolated
// lets the rest of the UI remain usable with a mock backend in tests.
export {
  GetAgents, GetOpenSessions, GetOpenSessionRefs, GetShell, ShellInstalled, SetShell, NotifyBeep,
  DebugLog, ListSessions, ListHiddenSessions, ListSessionsForProviders, ListHiddenSessionsForProviders, RenameSession, DeleteSession,
  UnhideSession, AdoptSession, StartSession, StartNew, StartNewAgent, ListAgentProviders, OpenFolder, TermWrite, TermResize, TermKill,
  GetVersion, GetUsageSummary, GetAccountQuota, CheckForUpdate, UpdateToLatest,
  LoadPreferences, SetPreference,
  ListProjects, ListProjectFavorites, SetProjectFavorite, ChooseProjectDir, AddProject, DeleteProject,
} from '../../wailsjs/go/app/App';
