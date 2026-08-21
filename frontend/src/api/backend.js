// Single boundary for Wails-generated bindings. Keeping this import isolated
// lets the rest of the UI remain usable with a mock backend in tests.
export {
  GetAgents, GetOpenSessions, GetShell, ShellInstalled, SetShell, NotifyBeep,
  DebugLog, ListSessions, ListHiddenSessions, RenameSession, DeleteSession,
  UnhideSession, StartSession, StartNew, TermWrite, TermResize, TermKill,
  GetVersion, CheckForUpdate, UpdateToLatest,
} from '../../wailsjs/go/app/App';
