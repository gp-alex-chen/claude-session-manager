import { leafOf } from '../utils.js';
import { listSig, pairPendingSessions } from './pairing.js';
import { dirIdentity, renderHiddenSessions, renderSessionList, updateProjectUsageLabels } from './view.js';
import { renderProviderBadge } from './provider-icons.js';

export function createSessionController(deps) {
  const {
    state,
    backend,
    terminalController,
    paneController = null,
    agentController,
    listRoot,
    filterRoot = null,
    storageRef = null,
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
  let addProjectBound = false;
  let sessionsLoaded = false;
  let providers = [{ id: 'claude', name: 'Claude', hasStatus: true, hasUsage: true, newSessionRestorable: true }];
  const pendingAdoptions = new Map();
  let adoptionInFlight = null;
  let adoptionInFlightGeneration = null;
  let adoptionGeneration = 0;
  let filterGeneration = 0;
  const restoringIDs = new Set();
  const historyByProvider = new Map();
  let requestGeneration = 0;
  let hiddenRequestGeneration = 0;
  let newLaunchInFlight = false;
  let filterReady = false;

  function providerForID(id) {
    return state.sessionProviders.get(id) || 'claude';
  }

  function historyProviders() {
    const ids = new Set(state.selectedProviders);
    for (const [token, terminal] of state.terminals) {
      const provider = terminal.provider || providerForID(token);
      if (token.startsWith('new-') && provider !== 'claude') continue;
      ids.add(provider);
    }
    for (const item of state.pendingNew) if (!item.provider || item.provider === 'claude') ids.add('claude');
    for (const id of restoringIDs) ids.add(providerForID(id));
    return [...ids].sort();
  }

  function visibleHistory(list) {
    return list.filter((item) => state.selectedProviders.has(item.provider || 'claude')
      || restoringIDs.has(item.id)
      || state.terminals.has(state.realToNew.get(item.id) || item.id));
  }

  function renderProviderFilters() {
    if (!filterRoot) return;
    const focused = filterRoot.contains?.(documentRef.activeElement) ? documentRef.activeElement?.dataset.provider : null;
    filterRoot.innerHTML = '';
    for (const provider of providers) {
      const selected = state.selectedProviders.has(provider.id);
      const button = el('button', 'provider-filter');
      button.type = 'button';
      button.dataset.provider = provider.id;
      button.classList.toggle('selected', selected);
      button.setAttribute('aria-pressed', String(selected));
      button.setAttribute('aria-label', '显示 ' + provider.name + ' 会话');
      button.disabled = selected && state.selectedProviders.size === 1;
      button.title = button.disabled ? '至少保留一种会话来源' : '显示或隐藏 ' + provider.name + ' 历史会话';
      const icon = renderProviderBadge({ provider: provider.id, name: provider.name, el });
      if (icon.classList.contains('s-provider-icon')) button.appendChild(icon);
      button.appendChild(el('span', 'provider-filter-label', provider.name));
      button.addEventListener('click', () => toggleProvider(provider.id));
      filterRoot.appendChild(button);
      if (focused === provider.id && !button.disabled) button.focus?.();
    }
  }

  async function toggleProvider(id) {
    if (!providers.some((provider) => provider.id === id)) return;
    if (state.selectedProviders.has(id)) {
      if (state.selectedProviders.size === 1) return;
      state.selectedProviders.delete(id);
    } else state.selectedProviders.add(id);
    filterGeneration++;
    storageRef?.setItem('session-provider-filter', JSON.stringify([...state.selectedProviders].sort()));
    renderProviderFilters();
    hiddenCount.textContent = '';
    if (hiddenOpen) hiddenPanel.innerHTML = '';
    renderSessions(lastLoaded);
    await loadSessions();
  }

  function restoreProviderFilter() {
    let saved;
    try { saved = JSON.parse(storageRef?.getItem('session-provider-filter') || 'null'); } catch { /* Use defaults. */ }
    const available = new Set(providers.map((provider) => provider.id));
    const selected = Array.isArray(saved) ? saved.filter((id) => available.has(id)) : [];
    state.selectedProviders = new Set(selected.length ? selected : available);
    renderProviderFilters();
  }

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
    state.sessionProviders.set(sessionInfo.id, sessionInfo.provider || 'claude');
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
    if (opened) {
      opened.dir = sessionInfo.dir;
      opened.provider = sessionInfo.provider || 'claude';
    }
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
    const temporary = state.pendingNew.find((item) => item.token === id && item.provider && item.provider !== 'claude');
    if (temporary) {
      state.pendingNew = state.pendingNew.filter((item) => item !== temporary);
      terminalController.closeTab(id);
      renderSessions(lastLoaded);
      return;
    }
    const mappedToken = state.realToNew.get(id);
    pendingAdoptions.delete(mappedToken || id);
    if (mappedToken) {
      state.closedTokens.add(id);
      terminalController.closeTab(mappedToken);
    } else {
      terminalController.closeTab(id);
    }
    renderSessions(lastLoaded);
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
    if (state.pendingNew.some((item) => item.token === token && item.provider && item.provider !== 'claude')) {
      state.pendingNew = state.pendingNew.filter((item) => item.token !== token);
      renderSessions(lastLoaded);
    }
  }

  function pairNewSessions(list) {
    state.pendingNew = pairPendingSessions({
      pending: state.pendingNew,
      lastLoaded: historyByProvider.get('claude') || lastLoaded.filter((item) => (item.provider || 'claude') === 'claude'),
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

  async function readHistory(ids) {
    const response = typeof backend.ListSessionsForProviders === 'function'
      ? await backend.ListSessionsForProviders(ids) : await backend.ListSessions();
    // The array form only serves the legacy boundary used by older callers.
    if (Array.isArray(response)) return { sessions: response, succeededProviders: ids, warnings: [] };
    if (!response || !Array.isArray(response.sessions) || !Array.isArray(response.succeededProviders)) throw new Error('会话来源查询返回无效结果');
    return response;
  }

  async function commitHistory(result, request, selection) {
    if (request !== requestGeneration || selection !== filterGeneration) return false;
    if (result.succeededProviders.includes('claude')) {
      await pairNewSessions(result.sessions.filter((item) => (item.provider || 'claude') === 'claude'));
    }
    if (request !== requestGeneration || selection !== filterGeneration) return false;
    for (const id of result.succeededProviders) {
      historyByProvider.set(id, result.sessions.filter((item) => (item.provider || 'claude') === id));
    }
    return true;
  }

  function mergedHistory(result) {
    const updated = new Set(result.succeededProviders);
    return [...result.sessions.filter((item) => updated.has(item.provider || 'claude')),
      ...lastLoaded.filter((item) => !updated.has(item.provider || 'claude'))];
  }

  async function loadSessions({ quiet = false, skipUnchanged = false } = {}) {
    const request = ++requestGeneration;
    const selection = filterGeneration;
    try {
      const result = await readHistory(historyProviders());
      if (!(await commitHistory(result, request, selection))) return null;
      const list = mergedHistory(result);
      sessionsLoaded = true;
      if (!skipUnchanged || listSig(list) !== lastListSignature) renderSessions(list);
      if (result.warnings?.length) {
        if (!quiet) setStatus('读取会话失败: ' + result.warnings.join('; '), 'warn');
        return false;
      }
      return true;
    } catch (error) {
      if (request !== requestGeneration || selection !== filterGeneration) return null;
      if (quiet) throw error;
      if (!quiet) setStatus('加载会话失败: ' + error, 'warn');
      return false;
    }
  }

  async function autoRefreshSessions() {
    if (!filterReady || newLaunchInFlight || refreshInFlight) return;
    refreshInFlight = true;
    try { await loadSessions({ quiet: true, skipUnchanged: true }); }
    finally { refreshInFlight = false; }
  }

  function renderSessions(list) {
    lastLoaded = list;
    lastListSignature = listSig(list);
    for (const session of list) {
      state.sessionDirs.set(session.id, session.dir);
      state.sessionProviders.set(session.id, session.provider || 'claude');
    }
    const temporarySessions = state.pendingNew
      .filter((item) => item.provider && item.provider !== 'claude')
      .map((item) => ({ id: item.token, dir: item.dir, name: item.name, provider: item.provider,
        temporary: true, time: '临时终端 · 重启不恢复' }));
    const visibleList = [...visibleHistory(list), ...temporarySessions];
    if (!state.collapseAllDone && list.length) {
      state.collapseAllDone = true;
      for (const session of list) state.collapsedDirs.add(dirIdentity(session.dir));
    }

    renderSessionList({
      listRoot,
      list: visibleList,
      projects: state.projects,
      state,
      agentController,
      el,
      onStartNew: chooseNewAgent,
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
      favoriteProjects: state.projectFavorites,
      usageByProject: state.usageByProject,
    });
    onProjects?.(visibleHistory(list));
    refreshHidden(true);
    agentController.refreshAgents();
    refreshFoldState();
    agentController.renderUnreadMarks();
    paneController?.setSessionOptions?.(visibleList);
    syncActiveHighlight();
  }

  async function startNew(dir, provider = 'claude') {
    if (newLaunchInFlight) return;
    newLaunchInFlight = true;
    const lifecycle = adoptionGeneration;
    const targetPaneId = paneController?.getTargetPaneId?.();
    try {
      if (provider === 'claude' && typeof backend.ListSessionsForProviders === 'function') {
        const request = ++requestGeneration;
        const selection = filterGeneration;
        let result;
        try { result = await readHistory(['claude']); }
        catch (error) {
          if (request !== requestGeneration || selection !== filterGeneration) return;
          throw new Error('无法读取 Claude 历史，不能安全建立新会话身份: ' + error);
        }
        if (request !== requestGeneration || selection !== filterGeneration) return;
        if (!result.succeededProviders.includes('claude')) throw new Error('无法读取 Claude 历史，不能安全建立新会话身份');
        if (!(await commitHistory(result, request, selection))) return;
        renderSessions(mergedHistory(result));
      }
      const token = provider === 'claude'
        ? await backend.StartNew(dir)
        : await backend.StartNewAgent(provider, dir);
      const providerName = providers.find((item) => item.id === provider)?.name || provider;
      const label = (provider === 'claude' ? '' : providerName + ' ') + '新会话 ' + (++newCounter) + ' · ' + leafOf(dir);
      state.sessionProviders.set(token, provider);
      terminalController.openTab(token, label);
      const terminal = state.terminals.get(token);
      if (terminal) { terminal.dir = dir; terminal.provider = provider; }
      state.pendingNew.push(provider === 'claude' ? { token, dir } : { token, dir, provider, name: label });
      if (provider !== 'claude') renderSessions(lastLoaded);
      showTerminal(token, targetPaneId ? { paneId: targetPaneId } : {});
      setStatus(provider === 'claude' ? '已启动新会话: ' + leafOf(dir)
        : providerName + ' 临时终端已打开；已有历史可从列表恢复，新终端暂不自动恢复', 'ok');
    } catch (error) {
      if (lifecycle === adoptionGeneration) setStatus('新建失败: ' + error, 'warn');
    } finally {
      newLaunchInFlight = false;
    }
  }

  async function loadAgentProviders() {
    if (typeof backend.ListAgentProviders !== 'function') return;
    try {
      const list = await backend.ListAgentProviders();
      if (!Array.isArray(list) || !list.length) throw new Error('agent 列表为空');
      const valid = list.filter((item) => item?.id && item?.name);
      if (!valid.length) throw new Error('agent 列表为空');
      providers = valid;
      state.agentProviders = new Map(providers.map((item) => [item.id, item]));
    } catch (error) {
      setStatus('加载 agent 列表失败: ' + error, 'warn');
    }
  }

  function chooseNewAgent(dir, event) {
    if (providers.length === 1) return startNew(dir, providers[0].id);
    contextMenu.innerHTML = '';
    for (const provider of providers) {
      addContextItem('新建 ' + provider.name + ' 会话', () => startNew(dir, provider.id));
    }
    contextMenu.style.left = Math.min(event?.clientX || 20, windowRef.innerWidth - 180) + 'px';
    contextMenu.style.top = Math.min(event?.clientY || 20, windowRef.innerHeight - 120) + 'px';
    contextMenu.style.display = 'block';
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

  async function loadProjectFavorites() {
    if (typeof backend?.ListProjectFavorites !== 'function') {
      state.projectFavorites = [];
      if (sessionsLoaded) renderSessions(lastLoaded);
      return true;
    }
    try {
      const favorites = await backend.ListProjectFavorites();
      state.projectFavorites = Array.isArray(favorites) ? favorites : [];
      if (sessionsLoaded) renderSessions(lastLoaded);
      return true;
    } catch (error) {
      state.projectFavorites = [];
      if (sessionsLoaded) renderSessions(lastLoaded);
      setStatus('加载项目收藏失败: ' + error, 'warn');
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
      return callback();
    });
    contextMenu.appendChild(item);
  }

  function showContextMenu(x, y, target) {
    contextMenu.innerHTML = '';
    if (target.type === 'directory') {
      addContextItem(target.favorite ? '取消收藏' : '添加收藏', () => toggleProjectFavorite(target));
      addContextItem('打开文件夹', () => openFolder(target));
    } else if (target.type === 'session') {
      const provider = state.agentProviders?.get(target.provider || 'claude');
      if (provider?.hasRename !== false) addContextItem('重命名…', () => renameSession(target));
      addContextItem('归档（不再显示）', () => deleteSession(target), true);
    }
    contextMenu.style.left = Math.min(x, windowRef.innerWidth - 160) + 'px';
    contextMenu.style.top = Math.min(y, windowRef.innerHeight - 140) + 'px';
    contextMenu.style.display = 'block';
  }

  async function toggleProjectFavorite(target) {
    if (typeof backend?.SetProjectFavorite !== 'function') {
      setStatus('项目收藏功能不可用', 'warn');
      return false;
    }
    const favorite = !target.favorite;
    try {
      await backend.SetProjectFavorite(target.dir, favorite);
    } catch (error) {
      setStatus((favorite ? '添加收藏失败: ' : '取消收藏失败: ') + error, 'warn');
      return false;
    }
    const identity = dirIdentity(target.dir);
    const current = Array.isArray(state.projectFavorites) ? state.projectFavorites : [];
    state.projectFavorites = current.filter((dir) => dirIdentity(dir) !== identity);
    if (favorite) state.projectFavorites.unshift(target.dir);
    renderSessions(lastLoaded);
    setStatus(favorite ? '已收藏项目: ' + leafOf(target.dir) : '已取消收藏: ' + leafOf(target.dir), 'ok');
    return true;
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
  }

  async function renameSession(target) {
    const current = state.sessionNames.get(target.id) || target.name;
    const input = windowRef.prompt(
      '重命名会话（同步到 agent 的原生会话名称，最多 120 个字符）',
      current,
    );
    if (input === null) return;
    const name = input.trim();
    if (!name) {
      setStatus('会话名称不能为空', 'warn');
      return;
    }
    try {
      await backend.RenameSession(target.id, name);
    } catch (error) {
      setStatus('重命名失败: ' + error, 'warn');
      return;
    }
    state.sessionNames.set(target.id, name);
    const token = state.realToNew.get(target.id) || target.id;
    const terminal = state.terminals.get(token);
    if (terminal) terminal.labelText = name;
    const refreshed = await loadSessions();
    if (refreshed === null) return;
    if (!refreshed) {
      setStatus('原生重命名成功，但会话列表刷新失败: ' + name, 'warn');
      return;
    }
    setStatus('已原生重命名: ' + name, 'ok');
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
    const generation = filterGeneration;
    const request = ++hiddenRequestGeneration;
    let list;
    try {
      const result = typeof backend.ListHiddenSessionsForProviders === 'function'
        ? await backend.ListHiddenSessionsForProviders([...state.selectedProviders].sort()) : await backend.ListHiddenSessions();
      list = Array.isArray(result) ? result : result?.sessions;
      if (!Array.isArray(result) && result?.warnings?.length) throw new Error(result.warnings.join('; '));
    } catch (error) {
      if (generation !== filterGeneration || request !== hiddenRequestGeneration) return;
      if (!silent || hiddenOpen) setStatus('加载归档失败: ' + error, 'warn');
      return;
    }
    if (generation !== filterGeneration || request !== hiddenRequestGeneration) return;
    list = Array.isArray(list) ? list : [];
    list = list.filter((item) => state.selectedProviders.has(item.provider || 'claude'));
    hiddenCount.textContent = list.length ? ' · ' + list.length : '';
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
        if ((await loadSessions()) === null) return;
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
    const lifecycle = adoptionGeneration;
    await loadAgentProviders();
    if (lifecycle !== adoptionGeneration) return;
    restoreProviderFilter();
    filterReady = true;
    await loadProjects();
    await loadProjectFavorites();
    await agentController.refreshAgents();
    if (lifecycle !== adoptionGeneration) return;
    let open;
    try {
      if (typeof backend.GetOpenSessionRefs === 'function') {
        const refs = await backend.GetOpenSessionRefs();
        if (lifecycle !== adoptionGeneration) return;
        open = [];
        for (const ref of Array.isArray(refs) ? refs : []) {
          if (!state.agentProviders.has(ref.provider)) continue;
          state.sessionProviders.set(ref.id, ref.provider);
          open.push(ref.id);
        }
      } else open = await backend.GetOpenSessions();
    } catch (error) {
      if (lifecycle !== adoptionGeneration) return;
      open = [];
      setStatus('读取待恢复会话失败: ' + error, 'warn');
    }
    if (lifecycle !== adoptionGeneration) return;
    open = Array.isArray(open) ? open : [];
    for (const id of open) restoringIDs.add(id);
    await loadSessions();
    if (lifecycle !== adoptionGeneration) { restoringIDs.clear(); return; }
    const paneIds = paneController?.getPaneFillOrder?.()
      || paneController?.getVisiblePaneIds?.()
      || [];
    let restoreIndex = 0;
    let restored = false;
    for (const id of open) {
      if (lifecycle !== adoptionGeneration) break;
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
      } finally {
        restoringIDs.delete(id);
      }
    }
    restoringIDs.clear();
    if (lifecycle !== adoptionGeneration) return;
    renderSessions(lastLoaded);
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
    filterReady = false;
    if (started) {
      started = false;
      clearIntervalFn(refreshTimer);
      refreshTimer = null;
    }
    adoptionGeneration += 1;
    filterGeneration += 1;
    requestGeneration += 1;
    hiddenRequestGeneration += 1;
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
    loadProjectFavorites,
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
    chooseNewAgent,
    toggleProjectFavorite,
    stop,
    syncActiveHighlight,
    toggleProvider,
    refreshSessionVisibility: () => renderSessions(lastLoaded),
  };
}
