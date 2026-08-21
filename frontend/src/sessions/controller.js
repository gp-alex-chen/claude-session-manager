import { leafOf } from '../utils.js';
import { listSig, pairPendingSessions } from './pairing.js';
import { renderHiddenSessions, renderSessionList } from './view.js';

export function createSessionController(deps) {
  const {
    state,
    backend,
    terminalController,
    agentController,
    listRoot,
    hiddenPanel,
    hiddenCount,
    hiddenButton,
    eyeButton,
    documentRef = typeof document === 'undefined' ? null : document,
    windowRef = typeof window === 'undefined' ? null : window,
    el,
    setStatus,
    setIntervalFn = setInterval,
    clearIntervalFn = clearInterval,
  } = deps;

  let lastLoaded = [];
  let lastListSignature = null;
  let hiddenOpen = false;
  let newCounter = 0;
  let refreshInFlight = false;
  let refreshTimer = null;
  let started = false;
  let ctxTarget = null;

  const contextMenu = documentRef.createElement('div');
  contextMenu.id = 'ctx-menu';
  documentRef.body.appendChild(contextMenu);

  function syncActiveHighlight() {
    for (const item of listRoot.querySelectorAll('.session-item')) {
      const token = state.realToNew.get(item.dataset.id) || item.dataset.id;
      item.classList.toggle('active', token === state.activeToken);
    }
  }

  function refreshFoldState() {
    for (const item of listRoot.querySelectorAll('.group .session-item')) {
      const id = item.dataset.id;
      const dir = item.dataset.dir;
      const kind = agentController.classifyAgent(id);
      const hidden = state.collapsedDirs.has(dir) && (state.eyeGlobalOff || kind === 'idle');
      item.classList.toggle('fold-hidden', hidden);
    }
  }

  async function openFromList(sessionInfo) {
    const token = state.realToNew.get(sessionInfo.id) || sessionInfo.id;
    const existing = state.terminals.get(token);
    if (existing && !existing.exited) {
      terminalController.activate(token);
      state.unreadSessions.delete(sessionInfo.id);
      agentController.renderUnreadMarks();
      return;
    }
    if (existing) terminalController.disposeSession(token);
    terminalController.openTab(token, sessionInfo.name);
    try {
      await backend.StartSession(sessionInfo.id, sessionInfo.dir);
      setStatus('已恢复: ' + sessionInfo.name, 'ok');
      state.unreadSessions.delete(sessionInfo.id);
      agentController.renderUnreadMarks();
      terminalController.activate(token);
    } catch (error) {
      setStatus('恢复失败: ' + error, 'warn');
      terminalController.disposeSession(token);
    }
  }

  function closeRealSession(id) {
    const mappedToken = state.realToNew.get(id);
    if (mappedToken) {
      state.closedTokens.add(id);
      terminalController.closeTab(mappedToken);
    } else {
      terminalController.closeTab(id);
    }
  }

  function pairNewSessions(list) {
    state.pendingNew = pairPendingSessions({
      pending: state.pendingNew,
      lastLoaded,
      list,
      realToNew: state.realToNew,
      newToReal: state.newToReal,
      onPair: (pendingItem, realId, info) => {
        const terminal = state.terminals.get(pendingItem.token);
        if (terminal && info) terminal.labelText = info.name;
        if (state.activeToken === pendingItem.token) syncActiveHighlight();
        return realId;
      },
    });
  }

  async function loadSessions() {
    let list;
    try {
      list = await backend.ListSessions();
    } catch (error) {
      setStatus('加载会话失败: ' + error, 'warn');
      return false;
    }
    renderSessions(Array.isArray(list) ? list : []);
    return true;
  }

  async function autoRefreshSessions() {
    if (refreshInFlight) return;
    refreshInFlight = true;
    try {
      const list = await backend.ListSessions();
      if (!Array.isArray(list)) return;
      const signature = listSig(list);
      if (signature === lastListSignature) return;
      pairNewSessions(list);
      renderSessions(list);
    } finally {
      refreshInFlight = false;
    }
  }

  function renderSessions(list) {
    lastLoaded = list;
    lastListSignature = listSig(list);
    if (!state.collapseAllDone && list.length) {
      state.collapseAllDone = true;
      for (const session of list) state.collapsedDirs.add(session.dir);
    }

    renderSessionList({
      listRoot,
      list,
      state,
      agentController,
      el,
      onStartNew: startNew,
      onToggleGroup: (dir, group, chevron) => {
        const collapsed = group.classList.toggle('collapsed');
        if (collapsed) state.collapsedDirs.add(dir);
        else state.collapsedDirs.delete(dir);
        chevron.classList.toggle('collapsed', collapsed);
        refreshFoldState();
      },
      onOpen: openFromList,
      onClose: closeRealSession,
      onContextMenu: showContextMenu,
    });
    refreshHidden(true);
    agentController.refreshAgents();
    refreshFoldState();
    agentController.renderUnreadMarks();
    syncActiveHighlight();
  }

  async function startNew(dir) {
    try {
      const token = await backend.StartNew(dir);
      const label = '新会话 ' + (++newCounter) + ' · ' + leafOf(dir);
      terminalController.openTab(token, label);
      const terminal = state.terminals.get(token);
      if (terminal) terminal.dir = dir;
      state.pendingNew.push({ token, dir });
      terminalController.activate(token);
      setStatus('已启动新会话: ' + leafOf(dir), 'ok');
    } catch (error) {
      setStatus('新建失败: ' + error, 'warn');
    }
  }

  function addContextItem(label, callback, danger) {
    const item = el('div', 'ctx-item' + (danger ? ' danger' : ''), label);
    item.addEventListener('click', () => {
      hideContextMenu();
      callback();
    });
    contextMenu.appendChild(item);
  }

  function showContextMenu(x, y, target) {
    ctxTarget = target;
    contextMenu.innerHTML = '';
    if (target.type === 'session') {
      addContextItem('重命名…', () => renameSession(target));
      addContextItem('归档（不再显示）', () => deleteSession(target), true);
    }
    contextMenu.style.left = Math.min(x, windowRef.innerWidth - 160) + 'px';
    contextMenu.style.top = Math.min(y, windowRef.innerHeight - 140) + 'px';
    contextMenu.style.display = 'block';
  }

  function hideContextMenu() {
    contextMenu.style.display = 'none';
    ctxTarget = null;
  }

  async function renameSession(target) {
    const current = state.sessionNames.get(target.id) || target.name;
    const input = windowRef.prompt(
      '重命名会话（留空 = 恢复原名）\n提示：这里只改本软件的显示名，真正改名请在 claude 会话内使用 /rename',
      current,
    );
    if (input === null) return;
    const name = input.trim();
    try {
      await backend.RenameSession(target.id, name);
    } catch (error) {
      setStatus('重命名失败: ' + error, 'warn');
      return;
    }
    state.sessionNames.set(target.id, name || target.name);
    const token = state.realToNew.get(target.id) || target.id;
    const terminal = state.terminals.get(token);
    if (terminal) terminal.labelText = name || target.name;
    await loadSessions();
    setStatus(name ? '已重命名: ' + name : '已恢复原名', 'ok');
  }

  async function deleteSession(target) {
    if (!windowRef.confirm('归档会话（不再显示）？\n会话文件不会被删除，可随时在顶部「归档」中恢复。')) return;
    try {
      await backend.DeleteSession(target.id);
    } catch (error) {
      setStatus('归档失败: ' + error, 'warn');
      return;
    }
    closeRealSession(target.id);
    state.sessionNames.delete(target.id);
    await loadSessions();
    setStatus('已归档（不再显示）: ' + target.name, 'warn');
  }

  async function refreshHidden(silent) {
    let list;
    try {
      list = await backend.ListHiddenSessions();
    } catch (error) {
      if (!silent) setStatus('加载归档失败: ' + error, 'warn');
      return;
    }
    list = Array.isArray(list) ? list : [];
    hiddenCount.textContent = list.length;
    if (!hiddenOpen) return;
    renderHiddenSessions({
      hiddenPanel,
      list,
      el,
      onRestore: async (session) => {
        try {
          await backend.UnhideSession(session.id);
        } catch (error) {
          setStatus('恢复失败: ' + error, 'warn');
          return;
        }
        await loadSessions();
        setStatus('已恢复显示: ' + session.name, 'ok');
      },
    });
  }

  function setEyeIcon(off) {
    eyeButton.innerHTML = off
      ? [
        '<svg viewBox="0 0 24 24" width="15" height="15" fill="none"',
        ' stroke="currentColor" stroke-width="1.8" stroke-linecap="round">',
        '<path d="M4 10.2C6 8.4 8.8 7.4 12 7.4s6 1 8 2.8"/>',
        '<path d="M4 13.8c2 1.8 4.8 2.8 8 2.8s6-1 8-2.8"/>',
        '<path d="M6.5 5.5l11 13"/></svg>',
      ].join('')
      : [
        '<svg viewBox="0 0 24 24" width="15" height="15" fill="none"',
        ' stroke="currentColor" stroke-width="1.8" stroke-linecap="round"',
        ' stroke-linejoin="round"><path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12',
        ' 18 18.5 12 18.5 2.5 12 2.5 12Z"/><circle cx="12" cy="12"',
        ' r="2.8"/></svg>',
      ].join('');
  }

  function paintEye() {
    eyeButton.classList.toggle('off', state.eyeGlobalOff);
    eyeButton.title = state.eyeGlobalOff
      ? '折叠时隐藏所有会话（点击开启：折叠时显示运行中的）'
      : '折叠时显示运行中的会话（点击关闭：折叠即全部隐藏）';
    setEyeIcon(state.eyeGlobalOff);
  }

  async function initialize() {
    await agentController.refreshAgents();
    await loadSessions();
    let open;
    try {
      open = await backend.GetOpenSessions();
    } catch (error) {
      return;
    }
    if (!Array.isArray(open)) return;
    for (const id of open) {
      const session = lastLoaded.find((item) => item.id === id);
      if (!session) continue;
      try {
        await openFromList(session);
      } catch (error) {
        setStatus('恢复失败: ' + error, 'warn');
      }
    }
  }

  function start() {
    if (started) return;
    started = true;
    refreshTimer = setIntervalFn(() => {
      autoRefreshSessions().catch(() => {});
    }, 5000);
  }

  function stop() {
    if (!started) return;
    started = false;
    clearIntervalFn(refreshTimer);
    refreshTimer = null;
  }

  hiddenButton.addEventListener('click', () => {
    hiddenOpen = !hiddenOpen;
    hiddenPanel.classList.toggle('hidden', !hiddenOpen);
    if (hiddenOpen) refreshHidden(true);
  });
  eyeButton.addEventListener('click', () => {
    state.eyeGlobalOff = !state.eyeGlobalOff;
    paintEye();
    refreshFoldState();
  });
  paintEye();
  documentRef.addEventListener('click', hideContextMenu);
  windowRef.addEventListener('blur', hideContextMenu);

  return {
    autoRefreshSessions,
    closeRealSession,
    deleteSession,
    initialize,
    listSig,
    loadSessions,
    openFromList,
    pairNewSessions,
    refreshFoldState,
    refreshHidden,
    renderSessions,
    renameSession,
    start,
    startNew,
    stop,
    syncActiveHighlight,
  };
}
