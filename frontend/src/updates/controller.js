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
  };
  let updateItem = null;
  let updateNote = null;

  function messageFor(error) {
    return String((error && error.message) || error);
  }

  function getSnapshot() {
    return {
      ...state,
      info: state.info ? { ...state.info } : null,
    };
  }

  function reset() {
    state.mode = 'idle';
    state.busy = false;
    state.pct = 0;
    state.phase = '';
    state.info = null;
    render();
  }

  function render() {
    if (!updateItem) return;
    updateItem.textContent = state.mode === 'ready'
      ? '发现新版本 ' + state.info.latest + ' → 点击更新并重启'
      : state.mode === 'checking'
        ? '正在检查…'
        : state.mode === 'applying'
          ? '更新中…'
          : '检查更新…';
    updateItem.classList.toggle('has-update', state.mode === 'ready');
    updateItem.classList.toggle('disabled', state.busy);
    for (const child of updateItem.querySelectorAll('.upd-col')) child.remove();

    if (state.mode === 'applying' && state.phase === '下载中') {
      const column = el('div', 'upd-col');
      const bar = el('div', 'upd-progress');
      const fill = el('div', 'upd-progress-fill');
      fill.style.width = state.pct + '%';
      bar.appendChild(fill);
      column.appendChild(bar);
      column.appendChild(el('div', 'upd-hint', '下载中 ' + state.pct + '%'));
      updateItem.appendChild(column);
    } else if (state.phase && !state.phase.includes('失败')) {
      const column = el('div', 'upd-col');
      column.appendChild(el('div', 'upd-hint', state.phase));
      updateItem.appendChild(column);
    }

    if (updateNote) {
      updateNote.textContent = state.mode === 'ready'
        ? '当前 v' + (state.info.current || 'dev') + '；将下载并自动重启，运行中的会话进程会结束'
        : '点击检查 GitHub 上是否有新版（v*-wails）';
    }
  }

  async function check() {
    if (state.busy) return;
    state.mode = 'checking';
    state.busy = true;
    state.phase = '检查中';
    render();
    try {
      const info = await backend.CheckForUpdate();
      if (info && info.hasUpdate) {
        state.mode = 'ready';
        state.busy = false;
        state.phase = '';
        state.info = { ...info };
        setStatus('发现新版本 ' + info.latest + '（当前 v' + (info.current || 'dev') + '）', 'warn');
      } else {
        state.info = null;
        state.busy = false;
        state.mode = 'idle';
        state.phase = '';
        setStatus('✅ 已是最新版本（v' + ((info && info.current) || 'dev') + '）', 'ok');
      }
    } catch (error) {
      state.info = null;
      state.busy = false;
      state.mode = 'idle';
      state.phase = '';
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
    render();
    try {
      await backend.UpdateToLatest();
      setStatus('✅ 更新完成', 'ok');
    } catch (error) {
      setStatus('❌ 更新失败: ' + messageFor(error), 'warn');
    }
    reset();
  }

  function handleState(phase) {
    state.phase = phase || '';
    if (phase === '下载失败' || phase === '更新失败' || phase === '检查失败') {
      state.mode = 'idle';
      state.busy = false;
      state.info = null;
      setStatus('❌ ' + phase, 'warn');
    } else if (phase === '重启中') {
      showToast('✅ 更新完成，正在重启…');
    }
    render();
  }

  function handleProgress(value) {
    state.pct = clampProgress(value);
    render();
  }

  function mount(menu) {
    const label = el('div', 'settings-group-label', '更新');
    updateItem = el('div', 'settings-item upd-run', '检查更新…');
    updateNote = el('div', 'settings-note upd-note-ver');
    updateItem.addEventListener('click', () => {
      if (state.mode === 'ready') return apply();
      if (!state.busy) return check();
      return undefined;
    });
    menu.appendChild(label);
    menu.appendChild(updateItem);
    menu.appendChild(updateNote);
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
  };
}
