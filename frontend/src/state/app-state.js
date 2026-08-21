// UI-only state container. Controllers can share this without importing DOM
// code or Wails bindings.
export function createAppState() {
  return {
    sessions: new Map(), closedTokens: new Set(), sessionNames: new Map(),
    collapsedDirs: new Set(), realToNew: new Map(), newToReal: new Map(),
    pendingNew: [], activeToken: null, collapseAllDone: false, eyeGlobalOff: false,
  };
}
