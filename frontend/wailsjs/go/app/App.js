// Wails binding for internal/app.App. Generated shape retained manually because
// the Wails CLI is not installed in this build environment.
export function GetAgents() { return window['go']['app']['App']['GetAgents'](); }
export function GetOpenSessions() { return window['go']['app']['App']['GetOpenSessions'](); }
export function GetShell() { return window['go']['app']['App']['GetShell'](); }
export function ShellInstalled(name) { return window['go']['app']['App']['ShellInstalled'](name); }
export function SetShell(name) { return window['go']['app']['App']['SetShell'](name); }
export function NotifyBeep() { return window['go']['app']['App']['NotifyBeep'](); }
export function DebugLog(msg) { return window['go']['app']['App']['DebugLog'](msg); }
export function ListSessions() { return window['go']['app']['App']['ListSessions'](); }
export function ListHiddenSessions() { return window['go']['app']['App']['ListHiddenSessions'](); }
export function RenameSession(id, name) { return window['go']['app']['App']['RenameSession'](id, name); }
export function DeleteSession(id) { return window['go']['app']['App']['DeleteSession'](id); }
export function UnhideSession(id) { return window['go']['app']['App']['UnhideSession'](id); }
export function StartNew(dir) { return window['go']['app']['App']['StartNew'](dir); }
export function StartSession(id, dir) { return window['go']['app']['App']['StartSession'](id, dir); }
export function TermKill(token) { return window['go']['app']['App']['TermKill'](token); }
export function TermResize(token, cols, rows) { return window['go']['app']['App']['TermResize'](token, cols, rows); }
export function TermWrite(token, b64) { return window['go']['app']['App']['TermWrite'](token, b64); }
export function GetVersion() { return window['go']['app']['App']['GetVersion'](); }
export function CheckForUpdate() { return window['go']['app']['App']['CheckForUpdate'](); }
export function UpdateToLatest() { return window['go']['app']['App']['UpdateToLatest'](); }
