import { b64ToBytes, bytesToB64 } from '../utils.js';
import { normalizeTerminalFontSize } from './options.js';

export function createTerminalController(deps) {
  const {
    state,
    backend,
    TerminalCtor,
    FitAddonCtor,
    termOptions,
    setStatus,
    appendHost,
    hostFactory,
    documentRef = typeof document === 'undefined' ? null : document,
    navigatorRef = typeof navigator === 'undefined' ? null : navigator,
    storageRef = typeof localStorage === 'undefined' ? null : localStorage,
    onActivate,
    onExit,
    onDispose,
  } = deps;

  const requestFrame = deps.requestFrame || ((callback) => {
    if (typeof globalThis.requestAnimationFrame === 'function') {
      return globalThis.requestAnimationFrame(callback);
    }
    return globalThis.setTimeout(callback, 0);
  });
  let fontFitPending = false;
  const fontFitTargets = new Set();
  const legacyVisibility = deps.layoutManaged !== true;

  const clipboardReader = deps.readClipboard || (() => {
    if (!navigatorRef?.clipboard?.readText) return Promise.reject(new Error('剪贴板不可用'));
    return navigatorRef.clipboard.readText();
  });
  const clipboardWriter = deps.writeClipboard || ((text) => {
    if (!navigatorRef?.clipboard?.writeText) return Promise.reject(new Error('剪贴板不可用'));
    return navigatorRef.clipboard.writeText(text);
  });

  function writeTerm(session, data) {
    backend.TermWrite(session.token, bytesToB64(new TextEncoder().encode(data)));
  }

  function hideNativeScrollbar(term) {
    const viewport = term?._core?.viewport;
    if (viewport && typeof viewport.scrollBarWidth === 'number') viewport.scrollBarWidth = 0;
  }

  async function pasteIntoTerm(session) {
    try {
      const text = await clipboardReader();
      if (text && session.term) session.term.paste(text);
    } catch (error) {
      setStatus?.('粘贴失败: ' + error, 'warn');
    }
  }

  async function handleContextMenu(session, event) {
    event.preventDefault();
    event.stopPropagation();
    const selection = session.term?.getSelection?.() || '';
    if (selection) {
      try {
        await clipboardWriter(selection);
        session.term.clearSelection();
      } catch (error) {
        setStatus?.('复制失败: ' + error, 'warn');
      }
      return;
    }
  }

  function openTab(token, name) {
    state.closedTokens.delete(token);
    const existing = state.terminals.get(token);
    if (existing) return existing;
    const session = {
      token,
      name,
      labelText: name,
      exited: false,
      visible: false,
      term: null,
      fit: null,
      host: hostFactory(),
    };
    session.host.classList.add('term-host');
    session.host.addEventListener('contextmenu', (event) => {
      void handleContextMenu(session, event);
    });
    appendHost(session.host);
    state.terminals.set(token, session);
    return session;
  }

  function mountSession(token, body, options = {}) {
    const session = state.terminals.get(token);
    if (!session) return null;
    if (body?.appendChild) body.appendChild(session.host);
    session.host.classList.toggle('is-mounted', true);
    session.visible = true;
    if (!session.term) makeTerminal(session);
    if (options.fit !== false) scheduleFontFit([token]);
    return session;
  }

  function unmountSession(token, hostPool) {
    const session = state.terminals.get(token);
    if (!session) return;
    session.visible = false;
    session.host.classList.toggle('is-mounted', false);
    if (hostPool?.appendChild) hostPool.appendChild(session.host);
  }

  function makeTerminal(session) {
    const term = new TerminalCtor(termOptions);
    const fit = new FitAddonCtor();
    term.loadAddon(fit);
    term.open(session.host);
    hideNativeScrollbar(term);
    session.term = term;
    session.fit = fit;
    if (!session.visible) term.resize(120, 32);

    term.onData((data) => writeTerm(session, data));
    term.attachCustomKeyEventHandler((event) => {
      if (event.type !== 'keydown') return true;
      const key = event.key.toLowerCase();
      if ((event.ctrlKey || event.metaKey) && key === 'v') {
        event.preventDefault();
        pasteIntoTerm(session);
        return false;
      }
      if (event.shiftKey && event.key === 'Insert') {
        event.preventDefault();
        pasteIntoTerm(session);
        return false;
      }
      if (event.ctrlKey && key === 'enter') {
        event.preventDefault();
        writeTerm(session, '\n');
        return false;
      }
      return true;
    });
    term.onResize(() => {
      if (session.visible && !session.resizeSuppressed) syncResize(session);
    });
    return term;
  }

  function syncResize(session) {
    if (!session?.term || !session.visible) return;
    const size = { cols: session.term.cols, rows: session.term.rows };
    if (session.lastResize
      && session.lastResize.cols === size.cols
      && session.lastResize.rows === size.rows) return;
    session.lastResize = size;
    backend.TermResize(session.token, size.cols, size.rows);
  }

  function fitAndSync(session) {
    try {
      if (!session?.term || !session.visible) return;
      session.resizeSuppressed = true;
      session.fit.fit();
    } catch (error) {
      // A hidden or disposed host can fail measurement during a resize.
      return;
    } finally {
      if (session) session.resizeSuppressed = false;
    }
    syncResize(session);
  }

  function activate(token) {
    const session = state.terminals.get(token);
    if (!session) return;
    if (!legacyVisibility) {
      focusSession(token);
      return;
    }
    state.activeToken = token;
    if (legacyVisibility) {
      for (const [currentToken, current] of state.terminals) {
        const visible = currentToken === token;
        current.host.classList.toggle('active', visible);
        current.visible = visible;
      }
    }
    state.unreadSessions.delete(token);
    onActivate?.(token);
    if (!session.term) makeTerminal(session);
    fitAndSync(session);
    session.term.focus();
  }

  function focusSession(token, options = {}) {
    const session = state.terminals.get(token);
    if (!session) return false;
    state.activeToken = token;
    state.unreadSessions.delete(token);
    onActivate?.(token);
    if (!session.term) makeTerminal(session);
    if (session.visible && options.resize !== false) fitAndSync(session);
    if (options.focus !== false) session.term.focus();
    return true;
  }

  function disposeSession(token) {
    const session = state.terminals.get(token);
    if (!session) return;
    state.terminals.delete(token);
    onDispose?.(token);
    if (session.term) {
      try { session.term.dispose(); } catch (error) { /* ignore */ }
    }
    session.host.remove();
  }

  function pickNextAfter(token) {
    const remaining = [...state.terminals.keys()];
    if (state.activeToken !== token) return;
    if (remaining.length) {
      activate(remaining[remaining.length - 1]);
    } else {
      state.activeToken = null;
      onActivate?.(null);
      setStatus?.('未运行 — 点击左侧会话恢复，或点分组行的新建会话按钮', '');
    }
  }

  function clearNewMapping(token) {
    const real = state.newToReal.get(token);
    if (real) {
      state.newToReal.delete(token);
      state.realToNew.delete(real);
    }
    const pendingIndex = state.pendingNew.findIndex((item) => item.token === token);
    if (pendingIndex >= 0) state.pendingNew.splice(pendingIndex, 1);
  }

  function closeTab(token) {
    state.closedTokens.add(token);
    const session = state.terminals.get(token);
    if (backend.TermKill) Promise.resolve(backend.TermKill(token)).catch(() => {});
    clearNewMapping(token);
    if (!session) return;
    disposeSession(token);
    if (legacyVisibility) pickNextAfter(token);
  }

  function resizeVisible(tokens = null) {
    const wanted = tokens || [...state.terminals.keys()];
    for (const token of wanted) {
      const session = state.terminals.get(token);
      if (session?.visible && session.term) fitAndSync(session);
    }
  }

  function resizeActive() {
    resizeVisible();
  }

  function scheduleFontFit(tokens = null) {
    const wanted = tokens || [...state.terminals.keys()];
    for (const token of wanted) {
      const session = state.terminals.get(token);
      if (session?.visible && session.term && session.fit) fontFitTargets.add(token);
    }
    if (!fontFitTargets.size) return;
    if (fontFitPending) return;

    fontFitPending = true;
    requestFrame(() => {
      fontFitPending = false;
      const pending = [...fontFitTargets];
      fontFitTargets.clear();
      for (const token of pending) {
        const session = state.terminals.get(token);
        if (session?.visible && session.term && session.fit) fitAndSync(session);
      }
    });
  }

  function handleData(token, b64) {
    if (state.closedTokens.has(token)) return;
    let session = state.terminals.get(token);
    if (!session) session = openTab(token, state.sessionNames.get(token) || '正在连接…');
    if (!session.term) makeTerminal(session);
    session.term.write(b64ToBytes(b64));
  }

  function handleExit(token) {
    if (state.closedTokens.has(token)) {
      onExit?.(token);
      return;
    }
    const real = state.newToReal.get(token);
    if (real) {
      state.newToReal.delete(token);
      state.realToNew.delete(real);
      disposeSession(token);
      if (legacyVisibility) pickNextAfter(token);
      onExit?.(token);
      return;
    }
    if (token.startsWith('new-')) {
      clearNewMapping(token);
      disposeSession(token);
      if (legacyVisibility) pickNextAfter(token);
      onExit?.(token);
      return;
    }
    const session = state.terminals.get(token);
    if (!session) {
      onExit?.(token);
      return;
    }
    session.exited = true;
    setStatus?.('会话已退出: ' + session.labelText, 'warn');
    onExit?.(token);
  }

  function applyTheme(name, notify = true) {
    const themes = deps.themes;
    const theme = themes[name] || themes.claude;
    state.currentTheme = themes[name] ? name : 'claude';
    try { storageRef?.setItem('term-theme', state.currentTheme); } catch (error) { /* ignore */ }
    termOptions.theme = theme;
    if (documentRef) documentRef.documentElement.style.setProperty('--term-bg', theme.background);
    for (const [, session] of state.terminals) {
      if (session.term) session.term.options.theme = theme;
    }
    if (notify) setStatus?.('终端配色已切换: ' + theme.name, 'ok');
  }

  function applyFontSize(value, notify = true) {
    const fontSize = normalizeTerminalFontSize(value);
    state.terminalFontSize = fontSize;
    termOptions.fontSize = fontSize;
    try { storageRef?.setItem('term-font-size', String(fontSize)); } catch (error) { /* ignore */ }
    for (const [, session] of state.terminals) {
      if (session.term) session.term.options.fontSize = fontSize;
    }
    scheduleFontFit();
    if (notify) setStatus?.('终端字号已调整: ' + fontSize + 'px', 'ok');
    return fontSize;
  }

  function getFontSize() {
    return normalizeTerminalFontSize(state.terminalFontSize);
  }

  return {
    applyFontSize,
    applyTheme,
    activate,
    closeTab,
    disposeSession,
    fitAndSync,
    getFontSize,
    handleData,
    handleExit,
    makeTerminal,
    mountSession,
    openTab,
    pasteIntoTerm,
    focusSession,
    resizeActive,
    resizeVisible,
    unmountSession,
    writeTerm,
  };
}
