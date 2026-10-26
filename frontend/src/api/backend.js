// Single boundary for Wails-generated bindings. Keeping this import isolated
// lets the rest of the UI remain usable with a mock backend in tests.
export {
  ReferenceCodexSession,
  GetAgents, GetOpenSessions, GetOpenSessionRefs, GetShell, ShellInstalled, SetShell, NotifyBeep, SetCompletionBadge, SetTaskbarStatus, SetNotificationAttention,
  DebugLog, ListSessions, ListHiddenSessions, ListSessionsForProviders, ListHiddenSessionsForProviders, RenameSession, DeleteSession,
  UnhideSession, AdoptSession, StartSession, StartNew, StartNewAgent, ListAgentProviders, OpenFolder, TermWrite, TermResize, TermKill,
  GetVersion, GetUsageSummary, GetAccountQuota, CheckForUpdate, UpdateToLatest,
  LoadPreferences, SetPreference,
  GetNotificationConfig, SetNotificationConfig, GetNotificationStatus, TestNotification,
  ListProjects, ListProjectFavorites, SetProjectFavorite, ChooseProjectDir, AddProject, DeleteProject,
} from '../../wailsjs/go/app/App';
