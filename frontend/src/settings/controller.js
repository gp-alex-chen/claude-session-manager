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

  function setPressed(button, selected) {
    button.setAttribute('aria-pressed', String(selected));
    button.classList.toggle('cur', selected);
  }

  function makeButton(className, text) {
    const button = el('button', className, text);
    button.type = 'button';
    return button;
  }

  function addHeading(panel, title, description) {
    panel.appendChild(el('h3', 'settings-section-title', title));
    if (description) panel.appendChild(el('p', 'settings-section-description', description));
  }

  function addThemeGroups(panel) {
    addHeading(panel, '界面模式', '选择应用界面的明暗主题。');
    const modeGroup = el('div', 'settings-mode-group');
    for (const [mode, text] of [['light', '日间模式'], ['dark', '夜间模式']]) {
      const item = makeButton('settings-segment', text);
      item.dataset.mode = mode;
      setPressed(item, mode === state.uiTheme);
      item.addEventListener('click', () => {
        applyUiTheme(mode);
        for (const button of modeGroup.children) setPressed(button, button.dataset.mode === mode);
      });
      modeGroup.appendChild(item);
    }
    panel.appendChild(modeGroup);

    addHeading(panel, '终端配色', '为新建和已有终端选择配色方案。');
    const themeGrid = el('div', 'settings-theme-grid');
    for (const [key, theme] of Object.entries(themes)) {
      const item = makeButton('settings-theme-card');
      item.dataset.theme = key;
      item.style.setProperty('--theme-bg', theme.background);
      item.style.setProperty('--theme-fg', theme.foreground);
      const preview = el('span', 'settings-theme-preview');
      preview.setAttribute('aria-hidden', 'true');
      const backgroundSwatch = el('span', 'settings-theme-swatch settings-theme-swatch-bg');
      const foregroundSwatch = el('span', 'settings-theme-swatch settings-theme-swatch-fg');
      preview.append(backgroundSwatch, foregroundSwatch);
      item.appendChild(preview);
      const label = el('span', 'settings-theme-name', theme.name);
      item.appendChild(label);
      setPressed(item, key === state.currentTheme);
      item.addEventListener('click', () => {
        state.currentTheme = key;
        terminalController.applyTheme(key);
        for (const button of themeGrid.children) setPressed(button, button.dataset.theme === key);
      });
      themeGrid.appendChild(item);
    }
    panel.appendChild(themeGrid);
  }

  function addShellGroup(generation, panel) {
    return Promise.all([
      Promise.resolve().then(() => backend.GetShell()).catch(() => 'cmd'),
      Promise.resolve().then(() => backend.ShellInstalled('pwsh')).catch(() => false),
    ]).then(([configuredShell, installed]) => {
      if (generation !== buildGeneration) return false;
      const shell = configuredShell === 'pwsh' ? 'pwsh' : 'cmd';
      addHeading(panel, '底层 Shell', '选择新启动或恢复会话使用的命令解释器。');
      const shellGrid = el('div', 'settings-shell-grid');
      const shellButtons = new Map();
      const setShellSelection = (selected) => {
        for (const [name, button] of shellButtons) setPressed(button, name === selected);
      };
      for (const [key, text, description] of [
        ['cmd', 'cmd.exe', 'Windows 自带，claude 退出后终端随之结束。'],
        ['pwsh', 'PowerShell 7', '需要 pwsh 在 PATH 中，claude 退出后停留在提示符。'],
      ]) {
        const item = makeButton('settings-shell-card');
        item.dataset.shell = key;
        item.appendChild(el('span', 'settings-shell-name', text));
        item.appendChild(el('span', 'settings-shell-description', description));
        const unavailable = key === 'pwsh' && !installed;
        item.disabled = unavailable;
        item.setAttribute('aria-disabled', String(unavailable));
        item.title = unavailable ? '未检测到 pwsh，请安装 PowerShell 7 并确保它在 PATH 中' : description;
        setPressed(item, key === shell);
        item.addEventListener('click', async () => {
          if (unavailable) return;
          try {
            await backend.SetShell(key);
          } catch (error) {
            setStatus('切换 Shell 失败: ' + error, 'warn');
            return;
          }
          if (generation !== buildGeneration) return;
          setShellSelection(key);
          setStatus('底层 Shell 已切换: ' + (key === 'pwsh' ? 'pwsh' : 'cmd') + '（新启动/恢复的会话生效）', 'ok');
        });
        shellButtons.set(key, item);
        shellGrid.appendChild(item);
      }
      panel.appendChild(shellGrid);
      if (!installed) {
        const message = shell === 'pwsh'
          ? '当前配置为 pwsh，但未检测到 pwsh；新启动的会话将以 cmd 兜底。'
          : 'pwsh 当前不可用；请安装 PowerShell 7 并确保它在 PATH 中。';
        panel.appendChild(el('div', 'settings-note', message));
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
