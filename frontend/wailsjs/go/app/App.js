// Wails-compatible wrapper for internal/app.App. It is committed so ordinary
// builds do not require the Wails CLI. If the CLI regenerates this file,
// review the method set and keep it aligned with src/api/backend.js.
export function GetAgents() { return window['go']['app']['App']['GetAgents'](); }
export function GetNotificationConfig() { return window['go']['app']['App']['GetNotificationConfig'](); }
export function SetNotificationConfig(config) { return window['go']['app']['App']['SetNotificationConfig'](config); }
export function GetNotificationStatus() { return window['go']['app']['App']['GetNotificationStatus'](); }
export function TestNotification(event) { return window['go']['app']['App']['TestNotification'](event); }
export function GetOpenSessions() { return window['go']['app']['App']['GetOpenSessions'](); }
export function GetOpenSessionRefs() { return window['go']['app']['App']['GetOpenSessionRefs'](); }
export function GetShell() { return window['go']['app']['App']['GetShell'](); }
export function ShellInstalled(name) { return window['go']['app']['App']['ShellInstalled'](name); }
export function SetShell(name) { return window['go']['app']['App']['SetShell'](name); }
export function NotifyBeep() { return window['go']['app']['App']['NotifyBeep'](); }
export function DebugLog(msg) { return window['go']['app']['App']['DebugLog'](msg); }
export function ListSessions() { return window['go']['app']['App']['ListSessions'](); }
export function ListHiddenSessions() { return window['go']['app']['App']['ListHiddenSessions'](); }
export function ListSessionsForProviders(providers) { return window['go']['app']['App']['ListSessionsForProviders'](providers); }
export function ListHiddenSessionsForProviders(providers) { return window['go']['app']['App']['ListHiddenSessionsForProviders'](providers); }
export function RenameSession(id, name) { return window['go']['app']['App']['RenameSession'](id, name); }
export function DeleteSession(id) { return window['go']['app']['App']['DeleteSession'](id); }
export function UnhideSession(id) { return window['go']['app']['App']['UnhideSession'](id); }
export function AdoptSession(runtimeToken, sessionID) { return window['go']['app']['App']['AdoptSession'](runtimeToken, sessionID); }
export function StartNew(dir) { return window['go']['app']['App']['StartNew'](dir); }
export function StartNewAgent(provider, dir) { return window['go']['app']['App']['StartNewAgent'](provider, dir); }
export function ListAgentProviders() { return window['go']['app']['App']['ListAgentProviders'](); }
export function StartSession(id, dir) { return window['go']['app']['App']['StartSession'](id, dir); }
export function OpenFolder(dir) { return window['go']['app']['App']['OpenFolder'](dir); }
export function TermKill(token) { return window['go']['app']['App']['TermKill'](token); }
export function TermResize(token, cols, rows) { return window['go']['app']['App']['TermResize'](token, cols, rows); }
export function TermWrite(token, b64) { return window['go']['app']['App']['TermWrite'](token, b64); }
export function GetVersion() { return window['go']['app']['App']['GetVersion'](); }
export function LoadPreferences() { return window['go']['app']['App']['LoadPreferences'](); }
export function SetPreference(key, value) { return window['go']['app']['App']['SetPreference'](key, value); }
export function GetUsageSummary(sessionID, projectDir) { return window['go']['app']['App']['GetUsageSummary'](sessionID, projectDir); }
export function GetAccountQuota(provider, force) { return window['go']['app']['App']['GetAccountQuota'](provider, force); }
export function CheckForUpdate() { return window['go']['app']['App']['CheckForUpdate'](); }
export function UpdateToLatest() { return window['go']['app']['App']['UpdateToLatest'](); }
export function ListProjects() { return window['go']['app']['App']['ListProjects'](); }
export function ListProjectFavorites() { return window['go']['app']['App']['ListProjectFavorites'](); }
export function SetProjectFavorite(dir, favorite) { return window['go']['app']['App']['SetProjectFavorite'](dir, favorite); }
export function ChooseProjectDir() { return window['go']['app']['App']['ChooseProjectDir'](); }
export function AddProject(dir) { return window['go']['app']['App']['AddProject'](dir); }
export function DeleteProject(dir) { return window['go']['app']['App']['DeleteProject'](dir); }
export function SetCompletionBadge(count, force) { return window['go']['app']['App']['SetCompletionBadge'](count, force); }
export function SetTaskbarStatus(count, attention, force) { return window['go']['app']['App']['SetTaskbarStatus'](count, attention, force); }
