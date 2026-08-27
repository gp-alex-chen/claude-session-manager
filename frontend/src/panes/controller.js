import { createPaneView } from './view.js';
import { normalizeLayoutMode, visiblePaneIds } from './presets.js';

const LAYOUT_STORAGE_KEY = 'terminal-layout-mode';

function readStorage(storage, key) {
  try { return storage?.getItem(key) || null; } catch (error) { return null; }
}

function writeStorage(storage, key, value) {
  try { storage?.setItem(key, value); } catch (error) { /* optional storage */ }
}

function paneById(state, id) {
  return state.panes.find((pane) => pane.id === id) || null;
}

function uniqueTokens(tokens) {
  const seen = new Set();
  return tokens.filter((token) => {
    if (!token || seen.has(token)) return false;
    seen.add(token);
    return true;
  });
}

export function createPaneController(deps) {
  const {
    state,
    terminalController,
    terminalRoot,
    statusBar,
    documentRef = typeof document === 'undefined' ? null : document,
    storageRef = typeof localStorage === 'undefined' ? null : localStorage,
    el,
    setStatus,
    onFocus,
  } = deps;
  const requestFrame = deps.requestFrame || ((callback) => {
    if (typeof globalThis.requestAnimationFrame === 'function') return globalThis.requestAnimationFrame(callback);
    return globalThis.setTimeout(callback, 0);
  });
  const ResizeObserverCtor = deps.ResizeObserverCtor || globalThis.ResizeObserver;

  let sessionOptions = [];
  let started = false;
  let currentMode = normalizeLayoutMode(readStorage(storageRef, LAYOUT_STORAGE_KEY));
  let resizeFramePending = false;
  const resizeTargets = new Set();

  const view = createPaneView({
    documentRef,
    terminalRoot,
    statusBar,
    el,
    onLayout: (mode) => setLayout(mode),
    onPaneFocus: (paneId, options) => focusPane(paneId, options),
    onSessionChange: (paneId, token) => {
      if (!token) {
        clearPane(paneId);
        return;
      }
      showSession(token, { paneId, focus: true });
    },
    onClearPane: (paneId) => clearPane(paneId),
  });

  const resizeObserver = typeof ResizeObserverCtor === 'function'
    ? new ResizeObserverCtor((entries) => {
      for (const entry of entries) {
        const paneId = entry.target?.dataset?.paneId;
        const pane = paneById(state, paneId);
        if (pane?.token && isVisiblePane(paneId)) resizeTargets.add(pane.token);
      }
      if (resizeFramePending || !resizeTargets.size) return;
      resizeFramePending = true;
      requestFrame(() => {
        resizeFramePending = false;
        const tokens = [...resizeTargets];
        resizeTargets.clear();
        terminalController.resizeVisible?.(tokens);
      });
    })
    : null;

  function visibleIds() {
    return visiblePaneIds(state.layoutMode);
  }

  function isVisiblePane(paneId) {
    return visibleIds().includes(paneId);
  }

  function findPaneByToken(token) {
    return state.panes.find((pane) => pane.token === token) || null;
  }

  function firstEmptyPane(ids = visibleIds()) {
    return ids.map((id) => paneById(state, id)).find((pane) => pane && !pane.token) || null;
  }

  function runningUnassignedTokens() {
    const assigned = new Set(state.panes.map((pane) => pane.token).filter(Boolean));
    return [...state.terminals.values()]
      .filter((session) => session && !session.exited && !assigned.has(session.token))
      .map((session) => session.token);
  }

  function sessionOptionList() {
    const options = [];
    const assigned = new Set(state.panes.map((pane) => pane.token).filter(Boolean));
    for (const session of state.terminals.values()) {
      if (!session || (session.exited && !assigned.has(session.token))) continue;
      const label = session.labelText || session.name || session.token;
      options.push({
        token: session.token,
        label: session.exited ? label + '（已退出）' : label,
        disabled: Boolean(session.exited),
      });
    }
    return options;
  }

  function syncView() {
    view.setLayout(state.layoutMode);
    const optionsByToken = new Map(sessionOptionList().map((item) => [item.token, item]));
    for (const option of sessionOptions) {
      const current = optionsByToken.get(option.token);
      if (!current) continue;
      optionsByToken.set(option.token, current.disabled ? current : option);
    }
    view.updatePanes(state.panes, [...optionsByToken.values()]);
    view.setFocusedPane(state.focusedPaneId);
  }

  function mountVisibleSessions() {
    const visible = new Set(visibleIds());
    for (const pane of state.panes) {
      if (!pane.token) continue;
      if (visible.has(pane.id)) {
        terminalController.mountSession?.(pane.token, view.paneBody(pane.id));
      } else {
        terminalController.unmountSession?.(pane.token, view.hostPool);
      }
    }
  }

  function focusPane(paneId, options = {}) {
    const ids = visibleIds();
    const selectedId = isVisiblePane(paneId) ? paneId : ids[0];
    if (!selectedId) return null;
    const pane = paneById(state, selectedId);
    if (!pane) return null;
    state.focusedPaneId = selectedId;
    state.activeToken = pane.token || null;
    view.setFocusedPane(selectedId);
    if (pane.token) {
      terminalController.focusSession?.(pane.token, { focus: options.focus !== false });
    } else {
      onFocus?.(null);
      setStatus?.('空窗格 — 从左侧选择会话', '');
    }
    return pane.token || null;
  }

  function focusFirstAssigned(options = {}) {
    const ids = visibleIds();
    const current = paneById(state, state.focusedPaneId);
    const preferred = current && ids.includes(current.id) && current.token
      ? current
      : ids.map((id) => paneById(state, id)).find((pane) => pane?.token);
    focusPane(preferred?.id || ids[0], options);
    syncView();
  }

  function targetPaneId(requestedPaneId) {
    if (requestedPaneId && isVisiblePane(requestedPaneId)) return requestedPaneId;
    if (isVisiblePane(state.focusedPaneId) && !paneById(state, state.focusedPaneId)?.token) {
      return state.focusedPaneId;
    }
    return firstEmptyPane()?.id || (isVisiblePane(state.focusedPaneId) ? state.focusedPaneId : visibleIds()[0]);
  }

  function showSession(token, options = {}) {
    const session = state.terminals.get(token);
    if (!session) return false;
    const existingPane = findPaneByToken(token);
    if (existingPane) {
      if (options.focus !== false) focusPane(existingPane.id, options);
      syncView();
      return true;
    }
    const pane = paneById(state, targetPaneId(options.paneId));
    if (!pane) return false;
    if (pane.token && pane.token !== token) {
      terminalController.unmountSession?.(pane.token, view.hostPool);
    }
    for (const other of state.panes) {
      if (other.token === token) other.token = null;
    }
    pane.token = token;
    mountVisibleSessions();
    syncView();
    if (options.focus !== false) focusPane(pane.id, options);
    return true;
  }

  function clearPane(paneId, options = {}) {
    const pane = paneById(state, paneId);
    if (!pane) return false;
    const token = pane.token;
    pane.token = null;
    if (token) terminalController.unmountSession?.(token, view.hostPool);
    syncView();
    if (options.focus !== false && state.focusedPaneId === paneId) focusFirstAssigned();
    return Boolean(token);
  }

  function removeSession(token) {
    let removed = false;
    for (const pane of state.panes) {
      if (pane.token !== token) continue;
      pane.token = null;
      removed = true;
    }
    if (!removed) return false;
    syncView();
    if (state.activeToken === token) focusFirstAssigned();
    return true;
  }

  function setLayout(mode) {
    const normalized = normalizeLayoutMode(mode);
    const oldTokens = uniqueTokens(state.panes.map((pane) => pane.token));
    const candidates = uniqueTokens([...oldTokens, ...runningUnassignedTokens()]);
    const ids = visiblePaneIds(normalized);
    for (const token of oldTokens) terminalController.unmountSession?.(token, view.hostPool);
    for (const pane of state.panes) pane.token = null;
    ids.forEach((id, index) => {
      const pane = paneById(state, id);
      if (pane) pane.token = candidates[index] || null;
    });
    state.layoutMode = normalized;
    currentMode = normalized;
    if (!ids.includes(state.focusedPaneId)) state.focusedPaneId = ids[0];
    mountVisibleSessions();
    syncView();
    writeStorage(storageRef, LAYOUT_STORAGE_KEY, normalized);
    focusFirstAssigned({ focus: false });
    return normalized;
  }

  function setSessionOptions(options) {
    const byToken = new Map(sessionOptionList().map((item) => [item.token, item]));
    if (Array.isArray(options)) {
      for (const item of options) {
        const token = item.token || state.realToNew.get(item.id) || item.id;
        if (!token || !byToken.has(token)) continue;
        const session = state.terminals.get(token);
        const label = item.label || item.name || item.id;
        byToken.set(token, {
          token,
          label: session?.exited ? label + '（已退出）' : label,
          disabled: Boolean(session?.exited),
        });
      }
    }
    sessionOptions = [...byToken.values()];
    syncView();
  }

  function resizeVisible() {
    terminalController.resizeVisible?.(visibleIds().map((id) => paneById(state, id)?.token).filter(Boolean));
  }

  function handleTerminalExit(token) {
    syncView();
    if (state.activeToken === token) setStatus?.('会话已退出', 'warn');
  }

  function refresh() {
    syncView();
  }

  function initialize() {
    state.layoutMode = currentMode;
    if (!visibleIds().includes(state.focusedPaneId)) state.focusedPaneId = visibleIds()[0];
    syncView();
    return Promise.resolve();
  }

  function start() {
    if (started) return;
    started = true;
    view.start();
    for (const { body } of view.paneBodies()) resizeObserver?.observe(body);
  }

  function stop() {
    if (!started) return;
    started = false;
    view.stop();
    resizeObserver?.disconnect?.();
    resizeTargets.clear();
    resizeFramePending = false;
  }

  syncView();

  return {
    clearPane,
    findPaneByToken,
    focusFirstAssigned,
    focusPane,
    getVisiblePaneIds: visibleIds,
    handleTerminalExit,
    initialize,
    removeSession,
    refresh,
    resizeVisible,
    setLayout,
    setSessionOptions,
    showSession,
    start,
    stop,
    getTargetPaneId: targetPaneId,
    view,
  };
}
