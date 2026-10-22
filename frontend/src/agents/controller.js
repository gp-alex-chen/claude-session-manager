import { paintProviderStatusIcon } from '../sessions/provider-icons.js';
import { terminalForSession } from '../sessions/pairing.js';

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
    onAgents,
    onTaskCompleted,
    onUnreadChange,
    setIntervalFn = setInterval,
    clearIntervalFn = clearInterval,
    setTimeoutFn = setTimeout,
    clearTimeoutFn = clearTimeout,
  } = deps;

  let refreshTimer = null;
  let started = false;
  let baselineReady = false;
  const completedCodexTurns = new Map();

  function classifyAgent(id) {
    const provider = state.sessionProviders?.get(id) || 'claude';
    const hasStatus = state.agentProviders?.get(provider)?.hasStatus ?? (provider === 'claude');
    if (!hasStatus) {
      const terminal = terminalForSession(state, id);
      return terminal && !terminal.exited ? 'unknown-open' : 'idle';
    }
    const agent = state.runningAgents.get(id) || state.runningAgents.get(state.realToNew.get(id));
    if (provider === 'codex') {
      const owner = state.newToReal.get(id);
      if (owner && owner !== id) return 'idle';
      if (!isBusy(agent) && agent?.state !== 'blocked' && ['failed', 'interrupted'].includes(agent?.turnStatus)) return agent.turnStatus;
      if (agent?.state === 'failed' || agent?.state === 'interrupted') return agent.state;
      if (!agent || agent.state === 'unknown') {
        const terminal = terminalForSession(state, id);
        return terminal && !terminal.exited ? 'unknown-open' : 'idle';
      }
    }
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
    const provider = badge.dataset.provider || state.sessionProviders.get(id) || 'claude';
    badge.dataset.provider = provider;
    badge.classList.remove('open', 'running', 'blocked', 'idle', 'ended-anim', 'unread');
    // Keep a busy badge's class and SVG in place so polling cannot restart its spin.
    if (kind !== 'working') badge.classList.remove('working');
    if (!['working', 'open', 'unknown-open', 'idle'].includes(kind)) badge.classList.remove('provider-status-icon');
    if (kind === 'working') {
      badge.classList.add('working');
      paintProviderStatusIcon(badge, provider);
      badge.title = '正在执行任务';
    } else if (kind === 'open') {
      badge.classList.add('open');
      paintProviderStatusIcon(badge, provider);
      badge.title = '已打开（交互会话，空闲等待输入）';
    } else if (kind === 'unknown-open') {
      badge.classList.add('open');
      paintProviderStatusIcon(badge, provider);
      badge.title = '终端已打开 · 任务状态未知';
    } else if (kind === 'blocked') {
      badge.classList.add('blocked');
      badge.textContent = '⚠';
      badge.title = state.runningAgents.get(id)?.waitingFor === 'userInput' ? '等待用户输入' : '等待权限批准';
    } else if (kind === 'failed' || kind === 'interrupted') {
      badge.classList.add('blocked');
      badge.textContent = kind === 'failed' ? '⚠' : '■';
      badge.title = kind === 'failed' ? '任务失败' : '任务已中断';
    } else if (kind === 'bg') {
      badge.classList.add('running');
      badge.textContent = '●';
      badge.title = '后台待命/排队';
    } else {
      badge.classList.add('idle');
      paintProviderStatusIcon(badge, provider);
      badge.title = '未打开';
    }
    describeBadge(badge, badge.title);
  }

  function describeBadge(badge, description) {
    const provider = badge.dataset.provider || state.sessionProviders.get(badge.dataset.id) || 'claude';
    const name = state.agentProviders.get(provider)?.name || provider[0].toUpperCase() + provider.slice(1);
    badge.title = name + ' · ' + description + (badge.dataset.temporary === 'true' ? ' · 临时终端，重启不自动恢复' : '');
    badge.setAttribute('role', 'img');
    badge.setAttribute('aria-label', badge.title);
  }

  function renderUnreadMarks() {
    onUnreadChange?.();
    if (!listRoot) return;
    for (const item of listRoot.querySelectorAll('.session-item')) {
      const id = item.dataset.id;
      const badge = item.querySelector('.badge');
      if (!badge) continue;
      if (state.unreadSessions.has(id)) {
        if (badge.classList.contains('ended-anim')) continue;
        if (!badge.classList.contains('unread')) {
          badge.classList.remove('working', 'open', 'running', 'blocked', 'idle', 'provider-status-icon');
          badge.classList.add('unread');
          badge.textContent = '●';
          describeBadge(badge, '已完成 · 未读（点击查看）');
        }
      } else if (badge.classList.contains('unread') || badge.classList.contains('ended-anim')) {
        repaintBadge(badge);
      }
    }
  }

  function markEnded(id, name, wasWorking, nativeID) {
    const activeToken = state.realToNew.get(id) || id;
    const foreground = documentRef?.visibilityState !== 'hidden' && (documentRef?.hasFocus?.() ?? true);
    if (state.activeToken !== activeToken || !foreground) state.unreadSessions.add(id);
    if (wasWorking) onTaskCompleted?.(id, nativeID);

    const item = listRoot?.querySelector(`.session-item[data-id="${id}"]`);
    const badge = item?.querySelector('.badge');
    if (badge && !badge.classList.contains('ended-anim')) {
      badge.classList.remove('working', 'provider-status-icon');
      badge.classList.add('ended-anim');
      badge.textContent = '─';
      describeBadge(badge, wasWorking ? '任务完成' : '会话结束');
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
    onAgents?.(safeList);
    const next = new Map();
    for (const agent of safeList) {
      const id = agent?.provider === 'codex' && agent.runtimeToken
        ? (state.newToReal.get(agent.runtimeToken) === agent.sessionId ? agent.sessionId : agent.runtimeToken)
        : agent?.sessionId;
      if (id) next.set(id, agent);
    }
    DebugLog('应用 agents=' + safeList.length + ' 条, 可识别' + next.size + ' 个');

    if (baselineReady) {
      for (const [id, previous] of state.previousRunningAgents) {
        if ((previous.provider || state.sessionProviders?.get(id)) === 'codex') continue;
        const current = next.get(id);
        const previousBusy = isBusy(previous);
        const currentBusy = isBusy(current);
        const previousDone = previous.state === 'done';
        const disappeared = !current;
        const unsuccessful = current && (['blocked', 'failed', 'interrupted', 'unknown'].includes(current.state)
          || current.status === 'waiting');
        const ended = !previousDone && !unsuccessful && (current?.state === 'done'
          || (previousBusy && (!current || !currentBusy))
          || (!previousBusy && previous.kind === 'interactive' && disappeared));
        const skip = state.closedTokens.has(id);
        if (ended && !state.endedAgents.has(id) && !skip) {
          state.endedAgents.add(id);
          DebugLog(`>>> 触发提示 ${id} 类型=${previousBusy ? '任务完成' : '会话结束'}`);
          markEnded(id, state.sessionNames.get(id) || id, previousBusy);
        }
      }
    }

    for (const [id, current] of next) {
      if (current.provider !== 'codex' || !current.turnId || current.turnStatus !== 'completed') continue;
      const turnKey = current.runtimeToken || id;
      if (completedCodexTurns.get(turnKey) === current.turnId) continue;
      completedCodexTurns.set(turnKey, current.turnId);
      if (baselineReady && !state.closedTokens.has(id)) {
        state.endedAgents.add(id);
        markEnded(id, state.sessionNames.get(id) || id, true, current.sessionId);
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
    refreshTimer = setIntervalFn(() => { refreshAgents(); }, 30000);
  }

  function stop() {
    if (!started) return;
    started = false;
    clearIntervalFn(refreshTimer);
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
