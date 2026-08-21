import { formatVersion } from '../utils.js';

export function createUpdateController(deps) {
  const {
    backend,
    el,
    setStatus,
    showToast,
    clampProgress,
  } = deps;

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

  function messageFor(error) {
    return String((error && error.message) || error);
  }

  function getSnapshot() {
    return {
      ...state,
      info: state.info ? { ...state.info } : null,
    };
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

  async function check() {
    if (state.busy) return;
    state.mode = 'checking';
    state.busy = true;
    state.phase = '检查中';
    state.statusText = '';
    render();
    try {
      const info = await backend.CheckForUpdate();
      if (info && info.hasUpdate) {
        state.mode = 'ready';
        state.busy = false;
        state.phase = '';
        state.statusText = '';
        state.info = { ...info };
        if (info.current) state.currentVersion = formatVersion(info.current);
        setStatus(
          '发现新版本 ' + formatVersion(info.latest) + '（当前 ' + formatVersion(info.current) + '）',
          'warn',
        );
      } else {
        state.info = null;
        state.busy = false;
        state.mode = 'idle';
        state.phase = '';
        state.statusText = '当前已是最新版本（' + formatVersion(info && info.current || state.currentVersion) + '）';
        if (info && info.current) state.currentVersion = formatVersion(info.current);
        setStatus('✅ 已是最新版本（' + formatVersion(info && info.current || state.currentVersion) + '）', 'ok');
      }
    } catch (error) {
      state.info = null;
      state.busy = false;
      state.mode = 'idle';
      state.phase = '';
      state.statusText = '检查失败：' + messageFor(error);
      setStatus('❌ ' + messageFor(error), 'warn');
    }
    render();
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
    getSnapshot,
    handleProgress,
    handleState,
    mount,
    reset,
    setCurrentVersion,
  };
}
