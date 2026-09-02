import { formatVersion } from '../utils.js';

export function createUpdateController(deps) {
  const {
    backend,
    el,
    setStatus,
    showToast,
    clampProgress,
    noticeNode,
    storage,
    nowFn = () => new Date(),
    setTimeoutFn = setTimeout,
    clearTimeoutFn = clearTimeout,
  } = deps;

  const dailyCheckStorageKey = 'update-last-check-date';

  const state = {
    mode: 'idle',
    busy: false,
    pct: 0,
    phase: '',
    info: null,
    currentVersion: 'vdev',
    statusText: '',
  };
  let card = null;
  let actionButton = null;
  let versionNode = null;
  let statusNode = null;
  let progressRegion = null;
  let progressBar = null;
  let warningNode = null;
  let dailyCheckTimer = null;
  let dailyCheckTask = null;
  let dailyCheckStarted = false;
  let dailyCheckGeneration = 0;

  function messageFor(error) {
    return String((error && error.message) || error);
  }

  function localDateKey(value) {
    const date = value instanceof Date ? value : new Date(value);
    if (Number.isNaN(date.getTime())) return null;
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, '0');
    const day = String(date.getDate()).padStart(2, '0');
    return year + '-' + month + '-' + day;
  }

  function readLastCheckDate() {
    try { return storage?.getItem(dailyCheckStorageKey) || null; } catch (error) { return null; }
  }

  function writeLastCheckDate(value) {
    try { storage?.setItem(dailyCheckStorageKey, value); } catch (error) { /* storage is optional */ }
  }

  function setNotice(info) {
    if (!noticeNode) return;
    const visible = Boolean(info && info.hasUpdate);
    noticeNode.hidden = !visible;
    noticeNode.setAttribute('aria-hidden', String(!visible));
    if (visible) {
      noticeNode.textContent = '有新版本';
      noticeNode.title = info.latest ? '发现新版本 ' + formatVersion(info.latest) : '发现新版本';
    } else {
      noticeNode.textContent = '';
      noticeNode.title = '';
    }
  }

  function millisecondsUntilNextLocalDay(value) {
    const date = value instanceof Date ? new Date(value.getTime()) : new Date(value);
    if (Number.isNaN(date.getTime())) return 24 * 60 * 60 * 1000;
    const next = new Date(date.getTime());
    next.setHours(24, 0, 0, 0);
    return Math.max(1000, next.getTime() - date.getTime());
  }

  function getSnapshot() {
    return {
      ...state,
      info: state.info ? { ...state.info } : null,
    };
  }

  function restoreSnapshot(snapshot) {
    Object.assign(state, {
      ...snapshot,
      info: snapshot.info ? { ...snapshot.info } : null,
    });
    setNotice(state.mode === 'ready' ? state.info : null);
    render();
  }

  function setCurrentVersion(value) {
    state.currentVersion = formatVersion(value);
    render();
  }

  function reset() {
    state.mode = 'idle';
    state.busy = false;
    state.pct = 0;
    state.phase = '';
    state.info = null;
    state.statusText = '';
    setNotice(null);
    render();
  }

  function render() {
    if (!actionButton) return;
    const isReady = state.mode === 'ready';
    const isApplying = state.mode === 'applying';
    const isDownloading = isApplying && state.phase === '下载中';

    actionButton.textContent = isReady
      ? '更新并重启'
      : state.mode === 'checking'
        ? '正在检查…'
        : isApplying
          ? state.phase === '重启中' ? '正在重启…' : '正在更新…'
          : '检查更新';
    actionButton.disabled = state.busy;
    actionButton.setAttribute('aria-disabled', String(state.busy));
    actionButton.classList.toggle('disabled', state.busy);
    if (card) {
      card.setAttribute('aria-busy', String(state.busy));
      card.classList.toggle('has-update', isReady);
    }
    if (versionNode) versionNode.textContent = state.currentVersion;

    const defaultStatus = state.mode === 'checking'
      ? '正在检查 GitHub 上是否有新版…'
      : state.mode === 'ready'
        ? '发现新版本 ' + formatVersion(state.info.latest)
        : isApplying
          ? (isDownloading ? '下载中 ' + state.pct + '%' : (state.phase || '正在准备更新…'))
          : '点击检查 GitHub 上是否有新版（v*-wails）';
    statusNode.textContent = state.statusText || defaultStatus;

    warningNode.hidden = !isReady;
    warningNode.setAttribute('aria-hidden', String(!isReady));
    progressRegion.hidden = !isDownloading;
    progressRegion.setAttribute('aria-hidden', String(!isDownloading));
    progressBar.setAttribute('aria-valuenow', String(state.pct));
    progressBar.style.width = state.pct + '%';
    progressBar.textContent = isDownloading ? '下载中 ' + state.pct + '%' : '';
  }

  async function check(options = {}) {
    if (state.busy) return null;
    const silent = options.silent === true;
    const isCurrent = typeof options.isCurrent === 'function' ? options.isCurrent : () => true;
    const previousSnapshot = getSnapshot();
    state.mode = 'checking';
    state.busy = true;
    state.phase = '检查中';
    state.statusText = '';
    render();
    try {
      const info = await backend.CheckForUpdate();
      if (!isCurrent()) {
        return null;
      }
      if (info && info.hasUpdate) {
        state.mode = 'ready';
        state.busy = false;
        state.phase = '';
        state.statusText = '';
        state.info = { ...info };
        if (info.current) state.currentVersion = formatVersion(info.current);
        setNotice(info);
        if (!silent) {
          setStatus(
            '发现新版本 ' + formatVersion(info.latest) + '（当前 ' + formatVersion(info.current) + '）',
            'warn',
          );
        }
      } else {
        state.info = null;
        state.busy = false;
        state.mode = 'idle';
        state.phase = '';
        state.statusText = '当前已是最新版本（' + formatVersion(info && info.current || state.currentVersion) + '）';
        if (info && info.current) state.currentVersion = formatVersion(info.current);
        setNotice(null);
        if (!silent) setStatus('✅ 已是最新版本（' + formatVersion(info && info.current || state.currentVersion) + '）', 'ok');
      }
      render();
      return info || null;
    } catch (error) {
      if (!isCurrent()) {
        return null;
      }
      if (silent && previousSnapshot.mode === 'ready' && previousSnapshot.info) {
        restoreSnapshot(previousSnapshot);
        return null;
      }
      state.info = null;
      state.busy = false;
      state.mode = 'idle';
      state.phase = '';
      state.statusText = '检查失败：' + messageFor(error);
      setNotice(null);
      if (!silent) setStatus('❌ ' + messageFor(error), 'warn');
      render();
      return null;
    }
  }

  async function checkDaily() {
    const today = localDateKey(nowFn());
    if (!today || readLastCheckDate() === today) return null;
    if (dailyCheckTask && dailyCheckTask.generation === dailyCheckGeneration) return dailyCheckTask.promise;
    dailyCheckTask = null;
    if (state.busy) return null;

    const task = {
      date: today,
      generation: dailyCheckGeneration,
      previousSnapshot: getSnapshot(),
      promise: null,
    };
    task.promise = check({
      silent: true,
      isCurrent: () => task.generation === dailyCheckGeneration,
    })
      .then((info) => {
        if (info && task.generation === dailyCheckGeneration) writeLastCheckDate(task.date);
        return info;
      })
      .finally(() => {
        if (dailyCheckTask === task) dailyCheckTask = null;
      });
    dailyCheckTask = task;
    return task.promise;
  }

  async function runDailyCheckAndCatchUp(generation) {
    const taskDate = dailyCheckTask && dailyCheckTask.generation === generation
      ? dailyCheckTask.date
      : localDateKey(nowFn());
    await checkDaily();
    if (!dailyCheckStarted || generation !== dailyCheckGeneration) return;
    const currentDate = localDateKey(nowFn());
    if (currentDate && currentDate !== taskDate && readLastCheckDate() !== currentDate) {
      await checkDaily();
    }
  }

  function scheduleDailyCheck(generation = dailyCheckGeneration) {
    if (!dailyCheckStarted || generation !== dailyCheckGeneration) return;
    if (dailyCheckTimer !== null) clearTimeoutFn(dailyCheckTimer);
    dailyCheckTimer = setTimeoutFn(async () => {
      dailyCheckTimer = null;
      if (!dailyCheckStarted || generation !== dailyCheckGeneration) return;
      await runDailyCheckAndCatchUp(generation);
      scheduleDailyCheck(generation);
    }, millisecondsUntilNextLocalDay(nowFn()));
  }

  function start() {
    if (dailyCheckStarted) return;
    dailyCheckStarted = true;
    dailyCheckGeneration += 1;
    const generation = dailyCheckGeneration;
    void checkDaily();
    scheduleDailyCheck(generation);
  }

  function stop() {
    dailyCheckStarted = false;
    dailyCheckGeneration += 1;
    if (dailyCheckTask && state.busy) restoreSnapshot(dailyCheckTask.previousSnapshot);
    dailyCheckTask = null;
    if (dailyCheckTimer !== null) clearTimeoutFn(dailyCheckTimer);
    dailyCheckTimer = null;
  }

  async function apply() {
    if (state.busy || state.mode !== 'ready') return;
    state.mode = 'applying';
    state.busy = true;
    state.pct = 0;
    state.phase = '';
    state.statusText = '';
    render();
    try {
      await backend.UpdateToLatest();
      setStatus('✅ 更新完成', 'ok');
      reset();
    } catch (error) {
      state.mode = 'idle';
      state.busy = false;
      state.phase = '';
      state.info = null;
      state.statusText = '更新失败：' + messageFor(error);
      setNotice(null);
      setStatus('❌ 更新失败: ' + messageFor(error), 'warn');
      render();
    }
  }

  function handleState(phase) {
    state.phase = phase || '';
    if (phase === '下载失败' || phase === '更新失败' || phase === '检查失败') {
      state.mode = 'idle';
      state.busy = false;
      state.info = null;
      state.statusText = phase;
      setNotice(null);
      setStatus('❌ ' + phase, 'warn');
    } else if (phase === '重启中') {
      state.mode = 'applying';
      state.busy = true;
      state.statusText = '更新完成，正在重启…';
      showToast('✅ 更新完成，正在重启…');
    }
    render();
  }

  function handleProgress(value) {
    state.pct = clampProgress(value);
    render();
  }

  function onActionClick() {
    if (state.mode === 'ready') return apply();
    if (!state.busy) return check();
    return undefined;
  }

  function mount(panel) {
    actionButton?.removeEventListener?.('click', onActionClick);
    panel.innerHTML = '';
    card = el('section', 'update-card');
    card.setAttribute('aria-live', 'polite');
    const title = el('h3', 'update-title', '应用更新');
    const description = el('p', 'update-description', '保持应用处于最新版本。');
    versionNode = el('div', 'update-current-version');
    statusNode = el('p', 'update-status');
    statusNode.setAttribute('aria-live', 'polite');
    actionButton = el('button', 'update-action', '检查更新');
    actionButton.type = 'button';
    progressRegion = el('div', 'update-progress-region');
    progressRegion.setAttribute('aria-label', '下载进度');
    progressBar = el('div', 'update-progress-bar');
    progressBar.setAttribute('role', 'progressbar');
    progressBar.setAttribute('aria-valuemin', '0');
    progressBar.setAttribute('aria-valuemax', '100');
    progressRegion.appendChild(progressBar);
    warningNode = el('p', 'update-warning', '运行中的会话进程会结束，更新后应用将自动重启。');
    actionButton.addEventListener('click', onActionClick);
    card.append(title, description, versionNode, statusNode, actionButton, progressRegion, warningNode);
    panel.appendChild(card);
    render();
  }

  return {
    apply,
    check,
    checkDaily,
    getSnapshot,
    handleProgress,
    handleState,
    mount,
    reset,
    setCurrentVersion,
    start,
    stop,
  };
}
