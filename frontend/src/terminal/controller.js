import { b64ToBytes, bytesToB64 } from '../utils.js';

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
  } = deps;

  function writeTerm(session, data) {
    backend.TermWrite(session.token, bytesToB64(new TextEncoder().encode(data)));
  }

  function legacyReadClipboard() {
    if (!documentRef) return '';
    const textarea = documentRef.createElement('textarea');
    textarea.style.cssText = 'position:fixed;top:-9999px;left:-9999px;width:10px;height:10px;opacity:0;';
    documentRef.body.appendChild(textarea);
    textarea.focus();
    let text = '';
    try {
      if (documentRef.execCommand('paste')) text = textarea.value;
    } catch (error) {
      // Clipboard access is optional in WebView2.
    }
    textarea.remove();
    return text;
  }

  async function pasteIntoTerm(session) {
    let text = '';
    try {
      text = navigatorRef?.clipboard ? await navigatorRef.clipboard.readText() : '';
    } catch (error) {
      text = legacyReadClipboard();
    }
    if (!text) text = legacyReadClipboard();
    if (text && session.term) session.term.paste(text);
  }

  function openTab(token, name) {
    state.closedTokens.delete(token);
    const existing = state.terminals.get(token);
    if (existing) {
      activate(token);
      return existing;
    }
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
    appendHost(session.host);
    state.terminals.set(token, session);
    return session;
  }

  function makeTerminal(session) {
    const term = new TerminalCtor(termOptions);
    const fit = new FitAddonCtor();
    term.loadAddon(fit);
    term.open(session.host);
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
      if (session.visible) backend.TermResize(session.token, term.cols, term.rows);
    });
    return term;
  }

  function fitAndSync(session) {
    try {
      session.fit.fit();
      const cols = Math.max(2, session.term.cols - 1);
      if (cols !== session.term.cols) session.term.resize(cols, session.term.rows);
      backend.TermResize(session.token, session.term.cols, session.term.rows);
    } catch (error) {
      // A hidden or disposed host can fail measurement during a resize.
    }
  }

  function activate(token) {
    const session = state.terminals.get(token);
    if (!session) return;
    state.activeToken = token;
    for (const [currentToken, current] of state.terminals) {
      const visible = currentToken === token;
      current.host.classList.toggle('active', visible);
      current.visible = visible;
    }
    state.unreadSessions.delete(token);
    onActivate?.(token);
    if (!session.term) makeTerminal(session);
    fitAndSync(session);
    session.term.focus();
    setStatus?.(
      '当前会话: ' + session.labelText + (session.exited ? '（已退出）' : ''),
      session.exited ? 'warn' : 'ok',
    );
  }

  function disposeSession(token) {
    const session = state.terminals.get(token);
    if (!session) return;
    if (session.term) {
      try { session.term.dispose(); } catch (error) { /* ignore */ }
    }
    session.host.remove();
    state.terminals.delete(token);
  }

  function pickNextAfter(token) {
    const remaining = [...state.terminals.keys()];
    if (state.activeToken !== token) return;
    if (remaining.length) {
      activate(remaining[remaining.length - 1]);
    } else {
      state.activeToken = null;
      setStatus?.('未运行 — 点击左侧会话恢复，或点分组行 + 新建会话', '');
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
    pickNextAfter(token);
  }

  function resizeActive() {
    const session = state.terminals.get(state.activeToken);
    if (session?.term) fitAndSync(session);
  }

  function handleData(token, b64) {
    if (state.closedTokens.has(token)) return;
    let session = state.terminals.get(token);
    if (!session) session = openTab(token, state.sessionNames.get(token) || '正在连接…');
    if (!session.term) makeTerminal(session);
    session.term.write(b64ToBytes(b64));
  }

  function handleExit(token) {
    if (state.closedTokens.has(token)) return;
    const real = state.newToReal.get(token);
    if (real) {
      state.newToReal.delete(token);
      state.realToNew.delete(real);
      disposeSession(token);
      pickNextAfter(token);
      return;
    }
    if (token.startsWith('new-')) {
      clearNewMapping(token);
      disposeSession(token);
      pickNextAfter(token);
      return;
    }
    const session = state.terminals.get(token);
    if (!session) return;
    session.exited = true;
    setStatus?.('会话已退出: ' + session.labelText, 'warn');
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

  return {
    applyTheme,
    activate,
    closeTab,
    disposeSession,
    fitAndSync,
    handleData,
    handleExit,
    makeTerminal,
    openTab,
    pasteIntoTerm,
    resizeActive,
    writeTerm,
  };
}
