export function createCompletionController({ state, SetTaskbarStatus, SetNotificationAttention, getAttention = () => false, documentRef, windowRef, onRead }) {
  const completed = new Set();
  let started = false;
  let desired = 0;
  let desiredAttention = false;
  let desiredUnread = 0;
  let revision = 0;
  let applied = 0;
  let lastCount = null;
  let lastAttention = null;
  let lastNotificationCount = null;
  let lastNotificationWaiting = null;
  let forceNext = false;
  let writing = false;
  const foreground = () => documentRef?.visibilityState !== 'hidden' && (documentRef?.hasFocus?.() ?? true);
  const identity = token => state.newToReal.get(token)
    || state.runningAgents.get(token)?.sessionId
    || [...state.runningAgents.values()].find(agent => agent.runtimeToken === token)?.sessionId || token;

  async function write() {
    if (writing) return;
    writing = true;
    try {
      while (applied < revision) {
        const current = revision;
        const count = desired;
        const attention = desiredAttention;
        const unread = desiredUnread;
        const force = forceNext;
        forceNext = false;
        if (unread !== lastNotificationCount || attention !== lastNotificationWaiting || force) {
          try {
            await SetNotificationAttention?.(unread, attention, force);
            lastNotificationCount = unread;
            lastNotificationWaiting = attention;
          } catch { lastNotificationCount = null; lastNotificationWaiting = null; }
        }
        if (count !== lastCount || attention !== lastAttention || force) {
          try {
            await SetTaskbarStatus?.(count, attention, force);
            lastCount = count;
            lastAttention = attention;
          } catch { lastCount = null; lastAttention = null; }
        }
        applied = current;
      }
    } finally { writing = false; }
  }
  function sync(force = false) {
    desired = completed.size;
    desiredAttention = started && Boolean(getAttention());
    desiredUnread = started ? new Set([...completed, ...state.unreadSessions].map(identity)).size : 0;
    forceNext ||= force;
    revision++;
    void write();
  }
  function complete(id, nativeID) {
    if (!started) return;
    const token = state.realToNew.get(id) || id;
    if (state.activeToken === token && foreground()) return;
    completed.add(nativeID || identity(token));
    sync();
  }
  function activate(token = state.activeToken) {
    if (!started || !token || token !== state.activeToken || !foreground()) return;
    const id = identity(token);
    completed.delete(id);
    state.unreadSessions.delete(token);
    state.unreadSessions.delete(id);
    onRead?.();
    sync();
  }
  function dismiss(token) {
    if (!started) return;
    const id = identity(token);
    completed.delete(id);
    state.unreadSessions.delete(token);
    state.unreadSessions.delete(id);
    sync();
  }
  const onFocus = () => { activate(); sync(true); };
  function start() {
    if (started) return;
    started = true;
    windowRef?.addEventListener?.('focus', onFocus);
    documentRef?.addEventListener?.('visibilitychange', onFocus);
    sync(true);
  }
  function stop() {
    if (!started) return;
    started = false;
    windowRef?.removeEventListener?.('focus', onFocus);
    documentRef?.removeEventListener?.('visibilitychange', onFocus);
    completed.clear();
    sync(true);
  }
  return { complete, activate, dismiss, sync, start, stop, count: () => completed.size };
}
