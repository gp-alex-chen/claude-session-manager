const QUOTA_REFRESH_MS = 15 * 60_000;
const QUOTA_SOURCES = new Set(['auto', 'codex', 'claude']);

export function activeQuotaProvider(state) {
  const token = state.activeToken;
  const terminal = token ? state.terminals.get(token) : null;
  if (!terminal || terminal.exited) return null;
  return terminal.provider || state.sessionProviders.get(token)
    || state.sessionProviders.get(state.newToReal.get(token)) || 'claude';
}

export function createQuotaController({ state, view, GetAccountQuota, storage, GetCodexResetCredits, ConsumeCodexResetCredit,
  setIntervalFn = setInterval, clearIntervalFn = clearInterval, nowFn = () => Date.now() }) {
  const cache = new Map();
  const pending = new Map();
  let started = false; let timer = null; let generation = 0; let provider = null;
  let source = 'auto';
  const revisions = new Map();
  const revision = id => revisions.get(id) || 0;
  const advance = id => { const value = revision(id) + 1; revisions.set(id, value); return value; };
  let resetDetails = null; let resetLoading = null; let resetUsing = null; let resetMessage = '';
  const apiPaused = () => {
    const snapshot=cache.get(provider)?.snapshot;
    return snapshot?.authMode==='api' && snapshot?.status==='unsupported';
  };
  function syncTimer() {
    const polling=started && provider && !apiPaused();
    if (polling && timer===null) timer=setIntervalFn(()=>sync(),QUOTA_REFRESH_MS);
    if (!polling && timer!==null) {clearIntervalFn(timer);timer=null;}
  }
  function notify() {
    const entry = cache.get(provider);
    view.render({ provider, source, snapshot: entry?.snapshot || null,
      loading: !!pending.get(provider), error: entry?.error || '',
      resetDetails, resetLoading: !!resetLoading, resetUsing: !!resetUsing, resetMessage });
  }
  async function refresh(force = false) {
    const id = provider;
    if (id === 'codex' && (resetLoading || resetUsing)) return;
    if (!started || !id || pending.has(id)) return;
    if (!force && apiPaused()) return;
    const old = cache.get(id);
    if (!force && old && nowFn() - old.attempted < QUOTA_REFRESH_MS) return;
    const epoch = generation;
    const stamp = revision(id);
    const request = {};
    pending.set(id, request); notify();
    try {
      const snapshot = await GetAccountQuota(id, force);
      if (!snapshot || snapshot.provider !== id) throw new Error('invalid quota response');
      if (started && generation === epoch && revision(id) === stamp) {
        cache.set(id, { snapshot, attempted: nowFn(), error: '' });
        if (id === 'codex' && !resetDetails?.pending) resetDetails = null;
      }
    } catch {
      if (started && generation === epoch && revision(id) === stamp) cache.set(id, { snapshot: old?.snapshot || null,
        attempted: nowFn(), error: '额度查询失败，请稍后重试' });
    } finally {
      if (pending.get(id) === request) pending.delete(id);
      if (started && generation === epoch && revision(id) === stamp) {
        // Measure the next interval from completion, including manual reads.
        // A timer started before a slow response could otherwise skip a tick.
        if (id===provider && timer!==null) {clearIntervalFn(timer);timer=null;}
        syncTimer();
      }
      if (started && generation === epoch) notify();
    }
  }
  function sync({ keepOpen = false } = {}) {
    if (!started) return;
    const next = source === 'auto' ? activeQuotaProvider(state) : source;
    if (next !== provider) {
      provider = next;
      if (!keepOpen) view.close();
    }
    syncTimer();
    notify();
    if (started && provider) void refresh();
  }
  function setSource(value) {
    if (!started || !QUOTA_SOURCES.has(value)) return;
    source = value;
    storage?.setItem('quota-source', source);
    sync({ keepOpen: true });
  }
  async function loadResetCredits() {
    if (!started || provider !== 'codex' || resetLoading || resetUsing || !GetCodexResetCredits) return;
    const operation = {}; const epoch = generation; const stamp = advance('codex');
    resetLoading = operation; resetDetails = null; resetMessage = ''; notify();
    try {
      const result = await GetCodexResetCredits();
      if (!started || generation !== epoch || revision('codex') !== stamp) return;
      if (result?.snapshot?.provider !== 'codex') throw new Error('invalid reset details');
      resetDetails = result;
      cache.set('codex', { snapshot: result.snapshot, attempted: nowFn(), error: '' });
    } catch {
      if (started && generation === epoch && revision('codex') === stamp) resetMessage = '重置卡详情读取失败，请刷新后再试。';
    } finally {
      if (resetLoading === operation) resetLoading = null;
      if (started && generation === epoch) {
        if (provider === 'codex' && timer !== null) { clearIntervalFn(timer); timer = null; }
        syncTimer(); notify();
      }
    }
  }
  async function consumeResetCredit(token, creditId) {
    if (!started || provider !== 'codex' || resetUsing || resetLoading || !ConsumeCodexResetCredit
      || !token || resetDetails?.offerToken !== token) return;
    if (resetDetails.pending && creditId !== resetDetails.pendingCreditId) return;
    const operation = {}; const epoch = generation; const stamp = advance('codex');
    resetUsing = operation; resetMessage = ''; notify();
    try {
      const result = await ConsumeCodexResetCredit(token, creditId);
      if (!started || generation !== epoch || revision('codex') !== stamp) return;
      const known = ['reset', 'alreadyRedeemed', 'nothingToReset', 'noCredit'].includes(result?.outcome);
      resetMessage = result?.message || (known ? '使用请求已处理。' : '使用结果未知，请核对上次请求。');
      if (result?.snapshot?.provider === 'codex') {
        cache.set('codex', { snapshot: result.snapshot, attempted: nowFn(), error: '' });
      } else {
        const old = cache.get('codex')?.snapshot;
        cache.set('codex', { snapshot: old ? { ...old, stale: true, resetCredits: null } : null, attempted: 0, error: '最新额度尚未确认，请刷新' });
      }
      resetDetails = { ...resetDetails, snapshot: cache.get('codex')?.snapshot,
        offerToken: known ? '' : token, pending: !known, pendingCreditId: creditId };
    } catch {
      if (started && generation === epoch && revision('codex') === stamp) {
        resetMessage = '请求结果未确认，请核对上次请求；不会自动重试。';
        resetDetails = { ...resetDetails, pending: true, pendingCreditId: creditId };
      }
    } finally {
      if (resetUsing === operation) resetUsing = null;
      if (started && generation === epoch) {
        if (provider === 'codex' && timer !== null) { clearIntervalFn(timer); timer = null; }
        syncTimer(); notify();
      }
    }
  }
  function start() {
    if (started) return;
    const saved = storage?.getItem('quota-source');
    source = QUOTA_SOURCES.has(saved) ? saved : 'auto';
    started = true; generation++; view.start?.(); sync();
  }
  function stop() {
    if (!started) return;
    started = false; generation++; pending.clear(); cache.clear();
    resetDetails = null; resetLoading = null; resetUsing = null; resetMessage = ''; revisions.clear();
    if (timer !== null) clearIntervalFn(timer); timer = null;
    view.stop?.();
  }
  return { start, stop, sync, refresh, setSource, viewResetCredits: loadResetCredits, consumeResetCredit };
}
