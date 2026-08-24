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
  } = deps;

  let started = false;
  let refreshTimer = null;
  let sequence = 0;
  let lifecycle = 0;
  const projectVersions = new Map();
  const projectRequests = new Map();

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

  function setUnavailable(identity = { token: null, sessionID: '', projectDir: '' }) {
    state.usageToken = identity.token;
    state.usageSessionID = identity.sessionID;
    state.usageProjectDir = identity.projectDir;
    state.usageSummary = null;
    state.usageLoading = false;
    state.usageError = null;
    state.usageStale = false;
    notify();
  }

  function isCurrent(identity, requestID, lifecycleID) {
    return lifecycle === lifecycleID
      && sequence === requestID
      && sameIdentity(currentIdentity(), identity)
      && state.usageToken === identity.token
      && state.usageSessionID === identity.sessionID
      && state.usageProjectDir === identity.projectDir;
  }

  function refreshActive() {
    const identity = currentIdentity();
    if (!identity.token || !identity.projectDir) {
      sequence += 1;
      setUnavailable(identity);
      return Promise.resolve(null);
    }

    const previousIdentity = {
      token: state.usageToken,
      sessionID: state.usageSessionID,
      projectDir: state.usageProjectDir,
    };
    if (!sameIdentity(previousIdentity, identity)) {
      state.usageSummary = null;
      state.usageError = null;
      state.usageStale = false;
    }
    state.usageToken = identity.token;
    state.usageSessionID = identity.sessionID;
    state.usageProjectDir = identity.projectDir;
    state.usageLoading = true;
    const projectVersion = nextProjectVersion(identity.projectDir);
    notify();
    const requestID = ++sequence;
    const lifecycleID = lifecycle;
    let request;
    try {
      request = Promise.resolve(GetUsageSummary(identity.sessionID, identity.projectDir));
    } catch (error) {
      request = Promise.reject(error);
    }
    return request.then((summary) => {
      if (!isCurrent(identity, requestID, lifecycleID)) return null;
      if (!summary || typeof summary !== 'object') throw new Error('usage summary unavailable');
      state.usageSummary = summary;
      if (projectVersions.get(identity.projectDir) === projectVersion) {
        state.usageByProject.set(identity.projectDir, summary);
      }
      state.usageLoading = false;
      state.usageError = null;
      state.usageStale = false;
      notify();
      return summary;
    }).catch((error) => {
      if (!isCurrent(identity, requestID, lifecycleID)) return null;
      state.usageLoading = false;
      state.usageError = errorMessage(error);
      state.usageStale = true;
      notify();
      return null;
    });
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
          || projectVersions.get(projectDir) !== projectVersion
          || !summary
        ) return null;
        state.usageByProject.set(projectDir, summary);
        notify();
        return summary;
      }).catch(() => null).finally(() => {
        if (projectRequests.get(projectDir) === requestID) projectRequests.delete(projectDir);
      }));
    }
    return Promise.all(requests);
  }

  function onActivate(token) {
    state.activeToken = token || null;
    return refreshActive();
  }

  function start() {
    if (started) return;
    started = true;
    lifecycle += 1;
    view?.start?.();
    refreshTimer = setIntervalFn(() => { void refreshActive(); }, 5000);
    if (state.activeToken) void refreshActive();
  }

  function stop() {
    if (started) {
      started = false;
      clearIntervalFn(refreshTimer);
      refreshTimer = null;
    }
    lifecycle += 1;
    sequence += 1;
    projectRequests.clear();
    state.usageLoading = false;
    view?.stop?.();
    notify();
  }

  return {
    currentIdentity,
    onActivate,
    prefetchProjects,
    refreshActive,
    start,
    stop,
  };
}
