import { leafOf } from '../utils.js';
import { listSig, pairPendingSessions } from './pairing.js';
import { dirIdentity, renderHiddenSessions, renderSessionList, updateProjectUsageLabels } from './view.js';

export function createSessionController(deps) {
  const {
    state,
    backend,
    terminalController,
    paneController = null,
    agentController,
    listRoot,
    addProjectButton = null,
    hiddenPanel,
    hiddenCount,
    hiddenButton,
    eyeButton,
    documentRef = typeof document === 'undefined' ? null : document,
    windowRef = typeof window === 'undefined' ? null : window,
    el,
    setStatus,
    onPair,
    onProjects,
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
  let addProjectBound = false;
  let sessionsLoaded = false;
  const pendingAdoptions = new Map();
  let adoptionInFlight = null;
  let adoptionInFlightGeneration = null;
  let adoptionGeneration = 0;

  const contextMenu = documentRef.createElement('div');
  contextMenu.id = 'ctx-menu';
  documentRef.body.appendChild(contextMenu);

  function syncActiveHighlight() {
    for (const item of listRoot.querySelectorAll('.session-item')) {
      const token = state.realToNew.get(item.dataset.id) || item.dataset.id;
      item.classList.toggle('active', token === state.activeToken);
    }
  }

  function refreshUsageLabels() {
    updateProjectUsageLabels({ listRoot, usageByProject: state.usageByProject });
  }

  function showTerminal(token, options = {}) {
    if (options.show === false) {
      paneController?.refresh?.();
      return true;
    }
    if (paneController?.showSession) return paneController.showSession(token, options);
    if (options.focus !== false) terminalController.activate(token);
    return true;
  }

  function refreshFoldState() {
    for (const item of listRoot.querySelectorAll('.group .session-item')) {
      const id = item.dataset.id;
      const dir = dirIdentity(item.dataset.dir);
      const kind = agentController.classifyAgent(id);
      const hidden = state.collapsedDirs.has(dir) && (state.eyeGlobalOff || kind === 'idle');
      item.classList.toggle('fold-hidden', hidden);
    }
  }

  async function openFromList(sessionInfo, options = {}) {
    const token = state.realToNew.get(sessionInfo.id) || sessionInfo.id;
    const targetPaneId = paneController?.getTargetPaneId?.(options.paneId) || options.paneId;
    const showOptions = targetPaneId ? { ...options, paneId: targetPaneId } : options;
    state.sessionDirs.set(sessionInfo.id, sessionInfo.dir);
    const existingSession = state.terminals.get(token);
    if (existingSession) existingSession.dir = sessionInfo.dir;
    const existing = existingSession;
    if (existing && !existing.exited) {
      showTerminal(token, showOptions);
      state.unreadSessions.delete(sessionInfo.id);
      agentController.renderUnreadMarks();
      return true;
    }
    if (existing) terminalController.disposeSession(token);
    terminalController.openTab(token, sessionInfo.name);
    const opened = state.terminals.get(token);
    if (opened) opened.dir = sessionInfo.dir;
    try {
      await backend.StartSession(sessionInfo.id, sessionInfo.dir);
      setStatus('已恢复: ' + sessionInfo.name, 'ok');
      state.unreadSessions.delete(sessionInfo.id);
      agentController.renderUnreadMarks();
      showTerminal(token, showOptions);
      return true;
    } catch (error) {
      setStatus('恢复失败: ' + error, 'warn');
      terminalController.disposeSession(token);
      return false;
    }
  }

  function closeRealSession(id) {
    const mappedToken = state.realToNew.get(id);
    pendingAdoptions.delete(mappedToken || id);
    if (mappedToken) {
      state.closedTokens.add(id);
      terminalController.closeTab(mappedToken);
    } else {
      terminalController.closeTab(id);
    }
  }

  async function flushPendingAdoptions() {
    const requestedGeneration = adoptionGeneration;
    if (adoptionInFlight) {
      const inFlight = adoptionInFlight;
      if (adoptionInFlightGeneration === requestedGeneration) return inFlight;
      await inFlight;
      return flushPendingAdoptions();
    }
    if (!pendingAdoptions.size) return;
    const runGeneration = requestedGeneration;
    const run = (async () => {
      for (const [token, adoption] of pendingAdoptions) {
        if (adoptionGeneration !== runGeneration) return;
        try {
          await backend.AdoptSession(token, adoption.realId);
          if (adoptionGeneration !== runGeneration) return;
          if (pendingAdoptions.get(token) === adoption) pendingAdoptions.delete(token);
        } catch (error) {
          if (adoptionGeneration !== runGeneration) return;
          setStatus('会话持久化失败: ' + error, 'warn');
          break;
        }
      }
    })();
    adoptionInFlight = run;
    adoptionInFlightGeneration = runGeneration;
    try {
      await run;
    } finally {
      if (adoptionInFlight === run) {
        adoptionInFlight = null;
        adoptionInFlightGeneration = null;
      }
    }
  }

  function handleTerminalExit(token) {
    pendingAdoptions.delete(token);
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
        if (terminal && info) terminal.dir = info.dir;
        if (info) state.sessionDirs.set(realId, info.dir);
        if (state.activeToken === pendingItem.token) syncActiveHighlight();
        pendingAdoptions.set(pendingItem.token, { realId });
        onPair?.(pendingItem, realId, info);
        return realId;
      },
    });
    return flushPendingAdoptions();
  }

  async function loadSessions() {
    let list;
    try {
      list = await backend.ListSessions();
    } catch (error) {
      setStatus('加载会话失败: ' + error, 'warn');
      return false;
    }
    list = Array.isArray(list) ? list : [];
    await pairNewSessions(list);
    sessionsLoaded = true;
    renderSessions(list);
    return true;
  }

  async function autoRefreshSessions() {
    if (refreshInFlight) return;
    refreshInFlight = true;
    try {
      const list = await backend.ListSessions();
      if (!Array.isArray(list)) return;
      const signature = listSig(list);
      if (signature === lastListSignature) {
        await flushPendingAdoptions();
        return;
      }
      await pairNewSessions(list);
      renderSessions(list);
    } finally {
      refreshInFlight = false;
    }
  }

  function renderSessions(list) {
    lastLoaded = list;
    lastListSignature = listSig(list);
    for (const session of list) state.sessionDirs.set(session.id, session.dir);
    if (!state.collapseAllDone && list.length) {
      state.collapseAllDone = true;
      for (const session of list) state.collapsedDirs.add(dirIdentity(session.dir));
    }

    renderSessionList({
      listRoot,
      list,
      projects: state.projects,
      state,
      agentController,
      el,
      onStartNew: startNew,
      onToggleGroup: (dir, group) => {
        const identity = dirIdentity(dir);
        const collapsed = group.classList.toggle('collapsed');
        if (collapsed) state.collapsedDirs.add(identity);
        else state.collapsedDirs.delete(identity);
        refreshFoldState();
      },
      onOpen: openFromList,
      onClose: closeRealSession,
      onContextMenu: showContextMenu,
      usageByProject: state.usageByProject,
    });
    onProjects?.(list);
    refreshHidden(true);
    agentController.refreshAgents();
    refreshFoldState();
    agentController.renderUnreadMarks();
    paneController?.setSessionOptions?.(list);
    syncActiveHighlight();
  }

  async function startNew(dir) {
    const targetPaneId = paneController?.getTargetPaneId?.();
    try {
      const token = await backend.StartNew(dir);
      const label = '新会话 ' + (++newCounter) + ' · ' + leafOf(dir);
      terminalController.openTab(token, label);
      const terminal = state.terminals.get(token);
      if (terminal) terminal.dir = dir;
      state.pendingNew.push({ token, dir });
      showTerminal(token, targetPaneId ? { paneId: targetPaneId } : {});
      setStatus('已启动新会话: ' + leafOf(dir), 'ok');
    } catch (error) {
      setStatus('新建失败: ' + error, 'warn');
    }
  }

  async function loadProjects() {
    if (typeof backend?.ListProjects !== 'function') {
      state.projects = [];
      if (sessionsLoaded) renderSessions(lastLoaded);
      return true;
    }
    try {
      const projects = await backend.ListProjects();
      state.projects = Array.isArray(projects) ? projects : [];
      if (sessionsLoaded) renderSessions(lastLoaded);
      return true;
    } catch (error) {
      state.projects = [];
      if (sessionsLoaded) renderSessions(lastLoaded);
      setStatus('加载项目失败: ' + error, 'warn');
      return false;
    }
  }

  async function addProjectFromChooser() {
    if (typeof backend?.ChooseProjectDir !== 'function') return;
    let dir;
    try {
      dir = await backend.ChooseProjectDir();
    } catch (error) {
      setStatus('选择项目目录失败: ' + error, 'warn');
      return;
    }
    if (dir === '' || dir === undefined || dir === null) return;
    if (typeof backend?.AddProject !== 'function') return;
    try {
      await backend.AddProject(dir);
      await loadProjects();
    } catch (error) {
      setStatus('添加项目失败: ' + error, 'warn');
    }
  }

  function bindProjectButton() {
    if (addProjectBound || !addProjectButton || typeof addProjectButton.addEventListener !== 'function') return;
    addProjectBound = true;
    addProjectButton.addEventListener('click', addProjectFromChooser);
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
    if (target.type === 'directory') {
      addContextItem('打开文件夹', () => openFolder(target));
    } else if (target.type === 'session') {
      addContextItem('重命名…', () => renameSession(target));
      addContextItem('归档（不再显示）', () => deleteSession(target), true);
    }
    contextMenu.style.left = Math.min(x, windowRef.innerWidth - 160) + 'px';
    contextMenu.style.top = Math.min(y, windowRef.innerHeight - 140) + 'px';
    contextMenu.style.display = 'block';
  }

  async function openFolder(target) {
    if (typeof backend?.OpenFolder !== 'function') {
      setStatus('打开文件夹功能不可用', 'warn');
      return false;
    }
    try {
      await backend.OpenFolder(target.dir);
      return true;
    } catch (error) {
      setStatus('打开文件夹失败: ' + error, 'warn');
      return false;
    }
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
    const label = state.eyeGlobalOff
      ? '折叠时隐藏所有会话（点击开启：折叠时显示运行中的）'
      : '折叠时显示运行中的会话（点击关闭：折叠即全部隐藏）';
    eyeButton.title = label;
    eyeButton.setAttribute('aria-label', label);
    setEyeIcon(state.eyeGlobalOff);
  }

  async function initialize() {
    await loadProjects();
    await agentController.refreshAgents();
    await loadSessions();
    let open;
    try {
      open = await backend.GetOpenSessions();
    } catch (error) {
      return;
    }
    if (!Array.isArray(open)) return;
    const paneIds = paneController?.getPaneFillOrder?.()
      || paneController?.getVisiblePaneIds?.()
      || [];
    let restoreIndex = 0;
    let restored = false;
    for (const id of open) {
      const session = lastLoaded.find((item) => item.id === id);
      if (!session) continue;
      try {
        const restoreOptions = {
          paneId: paneIds[restoreIndex],
          focus: !paneController,
        };
        if (paneController && !restoreOptions.paneId) restoreOptions.show = false;
        const ok = await openFromList(session, restoreOptions);
        if (ok && paneIds[restoreIndex]) {
          restoreIndex += 1;
          restored = true;
        }
      } catch (error) {
        setStatus('恢复失败: ' + error, 'warn');
      }
    }
    if (paneController && restored) paneController.focusFirstAssigned?.();
  }

  function start() {
    if (started) return;
    started = true;
    refreshTimer = setIntervalFn(() => {
      autoRefreshSessions().catch(() => {});
    }, 5000);
  }

  function stop() {
    if (started) {
      started = false;
      clearIntervalFn(refreshTimer);
      refreshTimer = null;
    }
    adoptionGeneration += 1;
    pendingAdoptions.clear();
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
  bindProjectButton();
  documentRef.addEventListener('click', hideContextMenu);
  windowRef.addEventListener('blur', hideContextMenu);

  return {
    autoRefreshSessions,
    closeRealSession,
    deleteSession,
    initialize,
    listSig,
    loadSessions,
    loadProjects,
    openFromList,
    openFolder,
    pairNewSessions,
    handleTerminalExit,
    refreshFoldState,
    refreshHidden,
    renderSessions,
    refreshUsageLabels,
    renameSession,
    start,
    startNew,
    stop,
    syncActiveHighlight,
  };
}
