const BUSY_STATES = new Set(['working', 'queued']);

function isBusy(agent) {
  return agent && (agent.status === 'busy' || BUSY_STATES.has(agent.state));
}

export function createAgentController(deps) {
  const {
    state,
    GetAgents,
    DebugLog,
    NotifyBeep,
    setStatus,
    listRoot,
    documentRef = typeof document === 'undefined' ? null : document,
    refreshFoldState,
    setIntervalFn = setInterval,
    clearIntervalFn = clearInterval,
    setTimeoutFn = setTimeout,
    clearTimeoutFn = clearTimeout,
  } = deps;

  const ecgFrames = ['▁', '▂', '▃', '▅', '▇', '▅', '▃', '▂', '▁', '─', '─', '─'];
  let ecgTick = 0;
  let ecgTimer = null;
  let refreshTimer = null;
  let started = false;
  let baselineReady = false;

  function classifyAgent(id) {
    const agent = state.runningAgents.get(id);
    if (!agent || agent.state === 'done') return 'idle';
    if (isBusy(agent)) return 'working';
    if (agent.state === 'blocked' || agent.status === 'waiting') return 'blocked';
    if (agent.kind === 'interactive') return 'open';
    return 'bg';
  }

  function showToast(text) {
    if (!documentRef) return;
    let toast = documentRef.getElementById('toast');
    if (!toast) {
      toast = documentRef.createElement('div');
      toast.className = 'toast';
      toast.id = 'toast';
      documentRef.body.appendChild(toast);
    }
    toast.textContent = text;
    toast.classList.add('show');
    clearTimeoutFn(toast._tm);
    toast._tm = setTimeoutFn(() => toast.classList.remove('show'), 2800);
  }

  function repaintBadge(badge) {
    const id = badge.dataset.id;
    const kind = classifyAgent(id);
    badge.classList.remove('ecg', 'open', 'running', 'blocked', 'idle', 'ended-anim', 'unread');
    if (kind === 'working') {
      badge.classList.add('ecg', 'green');
      badge.textContent = ecgFrames[ecgTick];
      badge.title = '正在执行任务';
    } else if (kind === 'open') {
      badge.classList.add('open');
      badge.textContent = '◉';
      badge.title = '已打开（交互会话，空闲等待输入）';
    } else if (kind === 'blocked') {
      badge.classList.add('blocked');
      badge.textContent = '⚠';
      badge.title = '等待权限批准';
    } else if (kind === 'bg') {
      badge.classList.add('running');
      badge.textContent = '●';
      badge.title = '后台待命/排队';
    } else {
      badge.classList.add('idle');
      badge.textContent = '●';
      badge.title = '未运行';
    }
  }

  function renderUnreadMarks() {
    if (!listRoot) return;
    for (const item of listRoot.querySelectorAll('.session-item')) {
      const id = item.dataset.id;
      const badge = item.querySelector('.badge');
      if (!badge) continue;
      if (state.unreadSessions.has(id)) {
        if (badge.classList.contains('ended-anim')) continue;
        if (!badge.classList.contains('unread')) {
          badge.classList.remove('ecg', 'open', 'running', 'blocked', 'idle');
          badge.classList.add('unread');
          badge.textContent = '●';
          badge.title = '已完成 · 未读（点击查看）';
        }
      } else if (badge.classList.contains('unread') || badge.classList.contains('ended-anim')) {
        repaintBadge(badge);
      }
    }
  }

  function markEnded(id, name, wasWorking) {
    const activeToken = state.realToNew.get(id) || id;
    if (state.activeToken !== activeToken) state.unreadSessions.add(id);

    const item = listRoot?.querySelector(`.session-item[data-id="${id}"]`);
    const badge = item?.querySelector('.badge');
    if (badge && !badge.classList.contains('ended-anim')) {
      badge.classList.add('ended-anim');
      badge.textContent = '─';
      const animationBadge = badge;
      setTimeoutFn(() => {
        animationBadge.classList.remove('ended-anim');
        renderUnreadMarks();
      }, 900);
    }

    const label = wasWorking ? '任务完成' : '会话结束';
    showToast('✅ ' + label + '：' + name);
    setStatus(label + ': ' + name, 'ok');
    Promise.resolve(NotifyBeep?.()).catch(() => {});
    renderUnreadMarks();
  }

  function applyAgents(list) {
    const safeList = Array.isArray(list) ? list : [];
    const next = new Map();
    for (const agent of safeList) {
      if (agent?.sessionId) next.set(agent.sessionId, agent);
    }
    DebugLog('应用 agents=' + safeList.length + ' 条, 可识别' + next.size + ' 个');

    if (baselineReady) {
      for (const [id, previous] of state.previousRunningAgents) {
        const current = next.get(id);
        const previousBusy = isBusy(previous);
        const currentBusy = isBusy(current);
        const disappeared = !current;
        const ended = current?.state === 'done'
          || (previousBusy && (!current || !currentBusy))
          || (!previousBusy && previous.kind === 'interactive' && disappeared);
        const skip = state.closedTokens.has(id);
        if (ended && !state.endedAgents.has(id) && !skip) {
          state.endedAgents.add(id);
          DebugLog(`>>> 触发提示 ${id} 类型=${previousBusy ? '任务完成' : '会话结束'}`);
          markEnded(id, state.sessionNames.get(id) || id, previousBusy);
        }
      }
    }

    baselineReady = true;
    state.previousRunningAgents = new Map(next);
    state.runningAgents.clear();
    for (const [id, agent] of next) state.runningAgents.set(id, agent);

    for (const id of state.endedAgents) {
      if (isBusy(state.runningAgents.get(id))) state.endedAgents.delete(id);
    }

    renderUnreadMarks();
    refreshFoldState?.();
    repaintAllBadges();
  }

  function repaintAllBadges() {
    if (!listRoot) return;
    for (const badge of listRoot.querySelectorAll('.badge')) {
      if (badge.classList.contains('ended-anim') || state.unreadSessions.has(badge.dataset.id)) continue;
      repaintBadge(badge);
    }
  }

  async function refreshAgents() {
    let list;
    try {
      list = await GetAgents();
    } catch (error) {
      return;
    }
    applyAgents(list);
    return list;
  }

  function start() {
    if (started) return;
    started = true;
    ecgTimer = setIntervalFn(() => {
      ecgTick = (ecgTick + 1) % ecgFrames.length;
      listRoot?.querySelectorAll('.badge.ecg').forEach((badge) => {
        if (!badge.classList.contains('ended-anim')) badge.textContent = ecgFrames[ecgTick];
      });
    }, 120);
    refreshTimer = setIntervalFn(() => { refreshAgents(); }, 30000);
  }

  function stop() {
    if (!started) return;
    started = false;
    clearIntervalFn(ecgTimer);
    clearIntervalFn(refreshTimer);
    ecgTimer = null;
    refreshTimer = null;
  }

  return {
    applyAgents,
    classifyAgent,
    markEnded,
    refreshAgents,
    renderUnreadMarks,
    repaintBadge,
    showToast,
    start,
    stop,
  };
}
