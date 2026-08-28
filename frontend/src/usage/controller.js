import { visiblePaneIds } from '../panes/presets.js';

const REFRESH_AFTER_MS = 5_000;
const STALE_AFTER_MS = 15_000;

function errorMessage(error) {
  return error instanceof Error ? error.message : String(error || 'usage unavailable');
}

function sameIdentity(left, right) {
  return left.token === right.token
    && left.sessionID === right.sessionID
    && left.projectDir === right.projectDir;
}

export function createUsageController(deps) {
  const {
    state,
    GetUsageSummary,
    render = () => {},
    view = null,
    setIntervalFn = setInterval,
    clearIntervalFn = clearInterval,
    getVisibleAssignments,
    nowFn = () => Date.now(),
  } = deps;

  let started = false;
  let refreshTimer = null;
  let lifecycle = 0;
  const projectVersions = new Map();
  const projectSuccessVersions = new Map();
  const projectRequests = new Map();
  const tokenVersions = new Map();
  const inFlight = new Map();

  function notify() {
    render(state);
  }

  function nextProjectVersion(projectDir) {
    const version = (projectVersions.get(projectDir) || 0) + 1;
    projectVersions.set(projectDir, version);
    return version;
  }

  function pendingDir(token, sessionID) {
    const pending = state.pendingNew.find((item) => item.token === token);
    return pending?.dir || state.sessionDirs.get(sessionID) || '';
  }

  function identityForToken(token) {
    const normalizedToken = token || null;
    const realID = normalizedToken && state.newToReal.get(normalizedToken)
      ? state.newToReal.get(normalizedToken)
      : (normalizedToken?.startsWith('new-') ? '' : (normalizedToken || ''));
    const terminal = normalizedToken ? state.terminals.get(normalizedToken) : null;
    const projectDir = terminal?.dir || pendingDir(normalizedToken, realID);
    return { token: normalizedToken, sessionID: realID, projectDir };
  }

  function currentIdentity() {
    return identityForToken(state.activeToken);
  }

  function emptyEntry(identity) {
    return {
      identity,
      summary: null,
      loading: false,
      waiting: false,
      stale: false,
      error: null,
      updatedAt: null,
    };
  }

  function syncLegacyActive() {
    const identity = currentIdentity();
    const entry = identity.token ? state.usageByToken.get(identity.token) : null;
    state.usageToken = identity.token;
    state.usageSessionID = identity.sessionID;
    state.usageProjectDir = identity.projectDir;
    state.usageSummary = entry?.summary || null;
    state.usageLoading = Boolean(entry?.loading);
    state.usageError = entry?.error || null;
    state.usageStale = Boolean(entry?.stale);
  }

  function ensureEntry(identity) {
    if (!identity.token) return null;
    const existing = state.usageByToken.get(identity.token);
    if (existing && sameIdentity(existing.identity, identity)) return existing;
    const entry = emptyEntry(identity);
    state.usageByToken.set(identity.token, entry);
    return entry;
  }

  function setEntry(token, identity, changes) {
    const current = state.usageByToken.get(token);
    if (!current || !sameIdentity(current.identity, identity)) return null;
    const next = { ...current, ...changes, identity };
    state.usageByToken.set(token, next);
    if (state.activeToken === token) syncLegacyActive();
    notify();
    return next;
  }

  function nextTokenVersion(token) {
    const version = (tokenVersions.get(token) || 0) + 1;
    tokenVersions.set(token, version);
    return version;
  }

  function acceptProjectSummary(projectDir, version, summary) {
    const latestSuccess = projectSuccessVersions.get(projectDir) || 0;
    if (version < latestSuccess) return false;
    projectSuccessVersions.set(projectDir, version);
    state.usageByProject.set(projectDir, summary);
    return true;
  }

  function visibleTokens() {
    if (typeof getVisibleAssignments === 'function') {
      return [...new Set((getVisibleAssignments() || [])
        .map((item) => typeof item === 'string' ? item : item?.token)
        .filter(Boolean))];
    }
    const visible = new Set(visiblePaneIds(state.layoutMode));
    const assigned = (state.panes || [])
      .filter((pane) => visible.has(pane.id) && pane.token)
      .map((pane) => pane.token);
    return [...new Set(assigned.length ? assigned : [state.activeToken].filter(Boolean))];
  }

  function isCurrentRequest(token, identity, requestID, tokenVersion, lifecycleID) {
    return lifecycle === lifecycleID
      && tokenVersions.get(token) === tokenVersion
      && inFlight.get(token)?.requestID === requestID
      && sameIdentity(identityForToken(token), identity)
      && sameIdentity(state.usageByToken.get(token)?.identity, identity);
  }

  function refreshToken(token, options = {}) {
    const identity = identityForToken(token);
    if (!identity.token) return Promise.resolve(null);

    const previous = state.usageByToken.get(token);
    if (previous && !sameIdentity(previous.identity, identity)) {
      nextTokenVersion(token);
      inFlight.delete(token);
    }
    const entry = ensureEntry(identity);
    const now = nowFn();
    if (
      entry.summary
      && entry.updatedAt != null
      && now - entry.updatedAt >= STALE_AFTER_MS
      && !entry.stale
    ) {
      setEntry(token, identity, { stale: true });
    }
    const existingRequest = inFlight.get(token);
    if (
      options.dedupe !== false
      && existingRequest
      && sameIdentity(existingRequest.identity, identity)
    ) return existingRequest.promise;

    if (identity.sessionID === '' && identity.token.startsWith('new-')) {
      setEntry(token, identity, {
        loading: false,
        waiting: true,
        error: null,
        stale: false,
      });
      return Promise.resolve(null);
    }

    if (!identity.projectDir) {
      setEntry(token, identity, {
        summary: null,
        loading: false,
        waiting: false,
        error: null,
        stale: false,
        updatedAt: null,
      });
      return Promise.resolve(null);
    }

    const fresh = entry.updatedAt != null && now - entry.updatedAt < REFRESH_AFTER_MS;
    if (options.force !== true && fresh) {
      if (state.activeToken === token) syncLegacyActive();
      return Promise.resolve(entry.summary);
    }

    const tokenVersion = nextTokenVersion(token);
    const projectVersion = nextProjectVersion(identity.projectDir);
    const requestID = Symbol(token);
    const lifecycleID = lifecycle;
    setEntry(token, identity, { loading: true, waiting: false, error: null });
    let request;
    try {
      request = Promise.resolve(GetUsageSummary(identity.sessionID, identity.projectDir));
    } catch (error) {
      request = Promise.reject(error);
    }
    const promise = request.then((summary) => {
      if (!isCurrentRequest(token, identity, requestID, tokenVersion, lifecycleID)) return null;
      if (!summary || typeof summary !== 'object') throw new Error('usage summary unavailable');
      const updatedAt = nowFn();
      acceptProjectSummary(identity.projectDir, projectVersion, summary);
      setEntry(token, identity, {
        summary,
        loading: false,
        waiting: false,
        error: null,
        stale: false,
        updatedAt,
      });
      return summary;
    }).catch((error) => {
      if (!isCurrentRequest(token, identity, requestID, tokenVersion, lifecycleID)) return null;
      const current = state.usageByToken.get(token);
      setEntry(token, identity, {
        loading: false,
        error: errorMessage(error),
        stale: Boolean(current?.summary),
      });
      return null;
    }).finally(() => {
      if (inFlight.get(token)?.requestID === requestID) inFlight.delete(token);
    });
    inFlight.set(token, { identity, promise, requestID });
    return promise;
  }

  function refreshActive(options = {}) {
    const force = options.force === undefined ? true : options.force;
    const dedupe = options.dedupe === undefined ? false : options.dedupe;
    return refreshToken(state.activeToken, { force, dedupe });
  }

  function refreshVisible(options = {}) {
    const force = options.force === undefined ? true : options.force;
    return Promise.all(visibleTokens().map((token) => refreshToken(token, { force })));
  }

  function prefetchProjects(list) {
    if (!started || !Array.isArray(list)) return Promise.resolve([]);
    const representatives = new Map();
    for (const session of list) {
      if (session?.dir && session?.id && !representatives.has(session.dir)) {
        representatives.set(session.dir, session.id);
      }
    }
    const requests = [];
    for (const [projectDir, sessionID] of representatives) {
      if (projectRequests.has(projectDir)) continue;
      const projectVersion = nextProjectVersion(projectDir);
      const lifecycleID = lifecycle;
      const requestID = Symbol(projectDir);
      projectRequests.set(projectDir, requestID);
      let request;
      try {
        request = Promise.resolve(GetUsageSummary(sessionID, projectDir));
      } catch (error) {
        request = Promise.reject(error);
      }
      requests.push(request.then((summary) => {
        if (
          lifecycle !== lifecycleID
          || projectRequests.get(projectDir) !== requestID
          || !summary
        ) return null;
        if (acceptProjectSummary(projectDir, projectVersion, summary)) notify();
        return summary;
      }).catch(() => null).finally(() => {
        if (projectRequests.get(projectDir) === requestID) projectRequests.delete(projectDir);
      }));
    }
    return Promise.all(requests);
  }

  function onActivate(token) {
    state.activeToken = token || null;
    syncLegacyActive();
    notify();
    return refreshToken(state.activeToken, { force: false });
  }

  function start() {
    if (started) return;
    started = true;
    lifecycle += 1;
    view?.start?.();
    refreshTimer = setIntervalFn(() => { void refreshVisible({ force: true }); }, 5000);
    if (state.activeToken) void refreshVisible({ force: false });
  }

  function stop() {
    if (started) {
      started = false;
      clearIntervalFn(refreshTimer);
      refreshTimer = null;
    }
    lifecycle += 1;
    inFlight.clear();
    projectRequests.clear();
    for (const [token, entry] of state.usageByToken) {
      state.usageByToken.set(token, { ...entry, loading: false });
    }
    syncLegacyActive();
    view?.stop?.();
    notify();
  }

  function removeToken(token) {
    nextTokenVersion(token);
    inFlight.delete(token);
    state.usageByToken.delete(token);
    if (state.activeToken === token) {
      syncLegacyActive();
      notify();
    }
  }

  return {
    currentIdentity,
    onActivate,
    prefetchProjects,
    refreshActive,
    refreshToken,
    refreshVisible,
    removeToken,
    start,
    stop,
  };
}
