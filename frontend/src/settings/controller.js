import { formatVersion } from '../utils.js';

export function createSettingsController(deps) {
  const {
    state,
    backend,
    terminalController,
    themes,
    settingsButton,
    settingsMenu,
    settingsDialog,
    settingsClose,
    settingsVersion,
    categoryButtons = [],
    panels = {},
    documentRef,
    windowRef,
    storage,
    el,
    setStatus,
    updateController,
  } = deps;

  let started = false;
  let buildGeneration = 0;
  let activeCategory = 'appearance';
  const categoryHandlers = new Map();

  function isOpen() {
    return !settingsMenu.hidden;
  }

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

  function setCategory(category) {
    if (!panels[category]) return;
    activeCategory = category;
    for (const button of categoryButtons) {
      const selected = button.dataset.category === category;
      button.setAttribute('aria-selected', String(selected));
      button.classList.toggle('active', selected);
    }
    for (const [name, panel] of Object.entries(panels)) {
      panel.hidden = name !== category;
    }
  }

  function hide(restoreFocus = false) {
    buildGeneration += 1;
    settingsMenu.hidden = true;
    settingsMenu.setAttribute('aria-hidden', 'true');
    if (restoreFocus) settingsButton.focus?.();
  }

  function close(event) {
    event?.stopPropagation?.();
    hide(true);
  }

  function show() {
    settingsMenu.hidden = false;
    settingsMenu.setAttribute('aria-hidden', 'false');
    setCategory(activeCategory);
    void build();
  }

  function onSettingsClick(event) {
    event.stopPropagation();
    if (isOpen()) close();
    else show();
  }

  function onOverlayClick(event) {
    if (event.target === settingsMenu) close(event);
  }

  function onDialogClick(event) {
    event.stopPropagation();
  }

  function onEscape(event) {
    if (event.key === 'Escape' && isOpen()) close(event);
  }

  function onWindowBlur() {
    if (isOpen()) hide();
  }

  function addThemeGroups(panel) {
    panel.appendChild(el('div', 'settings-group-label', '界面外观'));
    for (const [mode, text] of [['light', '☀️ 日间模式'], ['dark', '🌙 夜间模式']]) {
      const item = el('div', 'settings-item' + (mode === state.uiTheme ? ' cur' : ''), text);
      item.dataset.mode = mode;
      item.addEventListener('click', () => {
        applyUiTheme(mode);
        close();
      });
      panel.appendChild(item);
    }

    panel.appendChild(el('div', 'settings-group-label', '终端配色'));
    for (const [key, theme] of Object.entries(themes)) {
      const item = el('div', 'settings-item' + (key === state.currentTheme ? ' cur' : ''), theme.name);
      item.dataset.theme = key;
      item.style.setProperty('--dot', theme.background);
      item.addEventListener('click', () => {
        terminalController.applyTheme(key);
        close();
      });
      panel.appendChild(item);
    }
  }

  function addShellGroup(generation, panel) {
    return Promise.resolve()
      .then(() => backend.GetShell())
      .catch(() => 'cmd')
      .then(async (configuredShell) => {
        if (generation !== buildGeneration) return false;
        const shell = configuredShell === 'pwsh' ? 'pwsh' : 'cmd';
        panel.appendChild(el('div', 'settings-group-label', '底层 Shell'));
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
            close();
            setStatus('底层 Shell 已切换: ' + (key === 'pwsh' ? 'pwsh' : 'cmd') + '（新启动/恢复的会话生效）', 'ok');
          });
          panel.appendChild(item);
        }

        if (shell === 'pwsh') {
          let installed = true;
          try { installed = await backend.ShellInstalled('pwsh'); } catch (error) { installed = false; }
          if (generation !== buildGeneration) return false;
          if (!installed) {
            panel.appendChild(el(
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
    for (const panel of Object.values(panels)) panel.innerHTML = '';
    addThemeGroups(panels.appearance);
    await addShellGroup(generation, panels.terminal);
    if (generation !== buildGeneration) return;
    updateController.mount(panels.update);
  }

  function start() {
    if (started) return;
    started = true;
    settingsButton.addEventListener('click', onSettingsClick);
    settingsMenu.addEventListener('click', onOverlayClick);
    settingsDialog.addEventListener('click', onDialogClick);
    settingsClose?.addEventListener('click', close);
    for (const button of categoryButtons) {
      const handler = (event) => {
        event.stopPropagation?.();
        setCategory(button.dataset.category);
      };
      categoryHandlers.set(button, handler);
      button.addEventListener('click', handler);
    }
    documentRef.addEventListener('keydown', onEscape);
    windowRef.addEventListener('blur', onWindowBlur);
    setCategory(activeCategory);
  }

  function stop() {
    if (!started) return;
    started = false;
    settingsButton.removeEventListener?.('click', onSettingsClick);
    settingsMenu.removeEventListener?.('click', onOverlayClick);
    settingsDialog.removeEventListener?.('click', onDialogClick);
    settingsClose?.removeEventListener?.('click', close);
    for (const [button, handler] of categoryHandlers) {
      button.removeEventListener?.('click', handler);
    }
    categoryHandlers.clear();
    documentRef.removeEventListener?.('keydown', onEscape);
    windowRef.removeEventListener?.('blur', onWindowBlur);
    hide();
  }

  async function initialize() {
    applyUiTheme(readStorage('ui-theme'));
    const savedTerminalTheme = readStorage('term-theme');
    state.currentTheme = themes[savedTerminalTheme] ? savedTerminalTheme : 'claude';
    terminalController.applyTheme(state.currentTheme, false);
    try {
      const version = await backend.GetVersion();
      const formatted = formatVersion(version);
      settingsButton.title = '设置 · ' + formatted;
      if (settingsVersion) settingsVersion.textContent = formatted;
    } catch (error) {
      settingsButton.title = '设置';
      if (settingsVersion) settingsVersion.textContent = '';
    }
  }

  return {
    applyUiTheme,
    build,
    close,
    initialize,
    open: show,
    start,
    stop,
  };
}
