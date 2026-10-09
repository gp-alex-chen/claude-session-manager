const QUOTA_REFRESH_MS = 15 * 60_000;

export function activeQuotaProvider(state) {
  const token = state.activeToken;
  const terminal = token ? state.terminals.get(token) : null;
  if (!terminal || terminal.exited) return null;
  return terminal.provider || state.sessionProviders.get(token)
    || state.sessionProviders.get(state.newToReal.get(token)) || 'claude';
}

export function createQuotaController({ state, view, GetAccountQuota,
  setIntervalFn = setInterval, clearIntervalFn = clearInterval, nowFn = () => Date.now() }) {
  const cache = new Map();
  const pending = new Map();
  let started = false; let timer = null; let generation = 0; let provider = null;
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
    view.render({ provider, snapshot: entry?.snapshot || null,
      loading: !!pending.get(provider), error: entry?.error || '' });
  }
  async function refresh(force = false) {
    const id = provider;
    if (!started || !id || pending.has(id)) return;
    if (!force && apiPaused()) return;
    const old = cache.get(id);
    if (!force && old && nowFn() - old.attempted < QUOTA_REFRESH_MS) return;
    const epoch = generation;
    const request = {};
    pending.set(id, request); notify();
    try {
      const snapshot = await GetAccountQuota(id, force);
      if (!snapshot || snapshot.provider !== id) throw new Error('invalid quota response');
      if (started && generation === epoch) cache.set(id, { snapshot, attempted: nowFn(), error: '' });
    } catch {
      if (started && generation === epoch) cache.set(id, { snapshot: old?.snapshot || null,
        attempted: nowFn(), error: '额度查询失败，请稍后重试' });
    } finally {
      if (pending.get(id) === request) pending.delete(id);
      if (started && generation === epoch) {
        // Measure the next interval from completion, including manual reads.
        // A timer started before a slow response could otherwise skip a tick.
        if (id===provider && timer!==null) {clearIntervalFn(timer);timer=null;}
        syncTimer();notify();
      }
    }
  }
  function sync() {
    if (!started) return;
    const next = activeQuotaProvider(state);
    if (next !== provider) { provider = next; view.close(); }
    syncTimer();
    notify();
    if (started && provider) void refresh();
  }
  function start() { if (started) return; started = true; generation++; view.start?.(); sync(); }
  function stop() {
    if (!started) return;
    started = false; generation++; pending.clear(); cache.clear();
    if (timer !== null) clearIntervalFn(timer); timer = null;
    view.stop?.();
  }
  return { start, stop, sync, refresh };
}
