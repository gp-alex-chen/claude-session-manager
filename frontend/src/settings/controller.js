export function createSettingsController(deps) {
  const {
    state,
    backend,
    terminalController,
    themes,
    settingsButton,
    settingsMenu,
    documentRef,
    windowRef,
    storage,
    el,
    setStatus,
    updateController,
  } = deps;

  let started = false;
  let buildGeneration = 0;

  function onSettingsClick(event) {
    event.stopPropagation();
    open();
  }

  function onDocumentClick() { hide(); }
  function onWindowBlur() { hide(); }

  function readStorage(key) {
    try {
      return storage?.getItem(key) || null;
    } catch (error) {
      return null;
    }
  }

  function writeStorage(key, value) {
    try { storage?.setItem(key, value); } catch (error) { /* storage is optional */ }
  }

  function applyUiTheme(mode) {
    const theme = mode === 'dark' ? 'dark' : 'light';
    state.uiTheme = theme;
    documentRef.documentElement.dataset.theme = theme;
    writeStorage('ui-theme', theme);
  }

  function hide() {
    buildGeneration += 1;
    settingsMenu.style.display = 'none';
  }

  function addThemeGroups() {
    settingsMenu.appendChild(el('div', 'settings-group-label', '界面外观'));
    for (const [mode, text] of [['light', '☀️ 日间模式'], ['dark', '🌙 夜间模式']]) {
      const item = el('div', 'settings-item' + (mode === state.uiTheme ? ' cur' : ''), text);
      item.dataset.mode = mode;
      item.addEventListener('click', () => {
        applyUiTheme(mode);
        hide();
      });
      settingsMenu.appendChild(item);
    }

    settingsMenu.appendChild(el('div', 'settings-group-label', '终端配色'));
    for (const [key, theme] of Object.entries(themes)) {
      const item = el('div', 'settings-item' + (key === state.currentTheme ? ' cur' : ''), theme.name);
      item.dataset.theme = key;
      item.style.setProperty('--dot', theme.background);
      item.addEventListener('click', () => {
        terminalController.applyTheme(key);
        hide();
      });
      settingsMenu.appendChild(item);
    }
  }

  function addShellGroup(generation) {
    return Promise.resolve()
      .then(() => backend.GetShell())
      .catch(() => 'cmd')
      .then(async (configuredShell) => {
        if (generation !== buildGeneration) return false;
        const shell = configuredShell === 'pwsh' ? 'pwsh' : 'cmd';
        settingsMenu.appendChild(el('div', 'settings-group-label', '底层 Shell'));
        for (const [key, text] of [['cmd', 'cmd.exe（默认）'], ['pwsh', 'pwsh（PowerShell 7）']]) {
          const item = el('div', 'settings-item' + (key === shell ? ' cur' : ''), text);
          item.dataset.shell = key;
          item.title = key === 'pwsh'
            ? '需要已安装 PowerShell 7（pwsh 在 PATH 中）；claude 退出后停留在 pwsh 提示符'
            : 'Windows 自带；claude 退出后终端随之结束';
          item.addEventListener('click', async () => {
            if (key === 'pwsh') {
              let installed = false;
              try { installed = await backend.ShellInstalled('pwsh'); } catch (error) { /* unavailable */ }
              if (!installed) {
                setStatus('未检测到 pwsh（PowerShell 7）：请先安装并确保 pwsh 在 PATH 中，或保持 cmd', 'warn');
                return;
              }
            }
            try {
              await backend.SetShell(key);
            } catch (error) {
              setStatus('切换 Shell 失败: ' + error, 'warn');
              return;
            }
            hide();
            setStatus('底层 Shell 已切换: ' + (key === 'pwsh' ? 'pwsh' : 'cmd') + '（新启动/恢复的会话生效）', 'ok');
          });
          settingsMenu.appendChild(item);
        }

        if (shell === 'pwsh') {
          let installed = true;
          try { installed = await backend.ShellInstalled('pwsh'); } catch (error) { installed = false; }
          if (generation !== buildGeneration) return false;
          if (!installed) {
            settingsMenu.appendChild(el(
              'div',
              'settings-note',
              '⚠ 当前系统未检测到 pwsh，新启动的会话将以 cmd 兜底，装好后自动恢复 pwsh',
            ));
          }
        }
        return true;
      });
  }

  async function build() {
    const generation = ++buildGeneration;
    settingsMenu.innerHTML = '';
    addThemeGroups();
    await addShellGroup(generation);
    if (generation !== buildGeneration) return;
    updateController.mount(settingsMenu);
  }

  function open() {
    const isOpen = settingsMenu.style.display === 'block';
    hide();
    if (isOpen) return;
    const rect = settingsButton.getBoundingClientRect();
    settingsMenu.style.left = rect.left + 'px';
    settingsMenu.style.bottom = (windowRef.innerHeight - rect.top + 4) + 'px';
    settingsMenu.style.display = 'block';
    void build();
  }

  async function initialize() {
    applyUiTheme(readStorage('ui-theme'));
    const savedTerminalTheme = readStorage('term-theme');
    state.currentTheme = themes[savedTerminalTheme] ? savedTerminalTheme : 'claude';
    terminalController.applyTheme(state.currentTheme, false);
    try {
      const version = await backend.GetVersion();
      settingsButton.title = '设置 · v' + version;
    } catch (error) {
      settingsButton.title = '设置';
    }
  }

  function start() {
    if (started) return;
    started = true;
    settingsButton.addEventListener('click', onSettingsClick);
    documentRef.addEventListener('click', onDocumentClick);
    windowRef.addEventListener('blur', onWindowBlur);
  }

  function stop() {
    if (!started) return;
    started = false;
    settingsButton.removeEventListener?.('click', onSettingsClick);
    documentRef.removeEventListener?.('click', onDocumentClick);
    windowRef.removeEventListener?.('blur', onWindowBlur);
    hide();
  }

  return {
    applyUiTheme,
    build,
    close: hide,
    initialize,
    open,
    start,
    stop,
  };
}
