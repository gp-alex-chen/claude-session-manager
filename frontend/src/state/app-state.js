export function createAppState() {
  return {
    terminals: new Map(),
    closedTokens: new Set(),
    sessionNames: new Map(),
    collapsedDirs: new Set(),
    activeToken: null,
    pendingNew: [],
    realToNew: new Map(),
    newToReal: new Map(),
    runningAgents: new Map(),
    unreadSessions: new Set(),
    endedAgents: new Set(),
    previousRunningAgents: new Map(),
    collapseAllDone: false,
    eyeGlobalOff: false,
    currentTheme: 'claude',
    uiTheme: 'light',
  };
}
