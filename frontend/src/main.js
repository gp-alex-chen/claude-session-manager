import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';
import './styles/style.css';
import {
  GetAgents, GetOpenSessions, GetShell, ShellInstalled, SetShell, NotifyBeep,
  DebugLog, ListSessions, ListHiddenSessions, RenameSession, DeleteSession,
  UnhideSession, StartSession, StartNew, TermWrite, TermResize, TermKill,
  GetVersion, CheckForUpdate, UpdateToLatest,
} from './api/backend.js';
import { createAppState } from './state/app-state.js';
import { createTermOptions, THEMES } from './themes/catalog.js';
import { createTerminalController } from './terminal/controller.js';
import { createAgentController } from './agents/controller.js';
import { createSessionController } from './sessions/controller.js';

// —— 多会话终端管理 ——
// 每个会话一个独立 xterm 实例；切换/关闭都在左侧列表操作，
// 后台会话继续运行并把输出写入自己（可能隐藏）的 Terminal。

const termStack = document.getElementById('terminal');
const statusEl = document.getElementById('status-bar');
const listEl = document.getElementById('session-list');

const state = createAppState();
const TERM_OPTS = createTermOptions();

// —— 日间/夜间 UI 模式（默认日间） ——
// 只影响外壳 UI（侧边栏/列表等）；终端配色由左下角设置菜单独立控制。
try {
  if (localStorage.getItem('ui-theme') === 'dark') state.uiTheme = 'dark';
} catch (e) { /* localStorage 不可用时保持默认日间 */ }
function applyUiTheme(mode) {
  state.uiTheme = mode;
  document.documentElement.dataset.theme = mode;
  try { localStorage.setItem('ui-theme', mode); } catch (e) {}
}
applyUiTheme(state.uiTheme);

// —— 工具 ——
function setStatus(msg, cls) {
  statusEl.innerHTML = '';
  statusEl.appendChild(document.createTextNode(msg));
  statusEl.className = cls || '';
}

function el(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
}
let sessionController;

const agentController = createAgentController({
  state,
  GetAgents,
  DebugLog,
  NotifyBeep,
  setStatus,
  listRoot: listEl,
  documentRef: document,
  refreshFoldState: () => sessionController?.refreshFoldState(),
});

const terminalController = createTerminalController({
  state,
  backend: { TermWrite, TermResize, TermKill },
  TerminalCtor: Terminal,
  FitAddonCtor: FitAddon,
  termOptions: TERM_OPTS,
  themes: THEMES,
  setStatus,
  hostFactory: () => document.createElement('div'),
  appendHost: (host) => termStack.appendChild(host),
  documentRef: document,
  onActivate: () => {
    sessionController?.syncActiveHighlight();
    agentController.renderUnreadMarks();
  },
});

window.addEventListener('resize', () => terminalController.resizeActive());

const hiddenPanel = document.getElementById('hidden-panel');
const hiddenCount = document.getElementById('hidden-count');
const hiddenButton = document.getElementById('btn-hidden');
const eyeButton = document.getElementById('btn-eye');
sessionController = createSessionController({
  state,
  backend: {
    ListSessions, ListHiddenSessions, RenameSession, DeleteSession, UnhideSession,
    StartSession, StartNew, GetOpenSessions,
  },
  terminalController,
  agentController,
  listRoot: listEl,
  hiddenPanel,
  hiddenCount,
  hiddenButton,
  eyeButton,
  documentRef: document,
  windowRef: window,
  el,
  setStatus,
});

window.runtime.EventsOn('agents:update', (list) => agentController.applyAgents(list));
agentController.start();

window.runtime.EventsOn('term:data', (token, b64) => {
  terminalController.handleData(token, b64);
});

window.runtime.EventsOn('term:exit', (token) => {
  terminalController.handleExit(token);
});

sessionController.start();
sessionController.initialize();

try {
  const saved = localStorage.getItem('term-theme');
  if (saved && THEMES[saved]) state.currentTheme = saved;
} catch (e) { /* localStorage 不可用时保持默认 */ }
terminalController.applyTheme(state.currentTheme, false);

// —— 左下角设置菜单：界面外观（日间/夜间）+ 终端配色（8 套） ——
const settingsBtn = document.getElementById('btn-settings');
const settingsMenu = document.getElementById('settings-menu');
function hideSettingsMenu() { settingsMenu.style.display = 'none'; }
function buildSettingsMenu() {
  settingsMenu.innerHTML = '';
  const label1 = el('div', 'settings-group-label', '界面外观');
  settingsMenu.appendChild(label1);
  for (const [mode, text] of [['light', '☀️ 日间模式'], ['dark', '🌙 夜间模式']]) {
    const it = el('div', 'settings-item' + (mode === state.uiTheme ? ' cur' : ''), text);
    it.dataset.mode = mode;
    it.addEventListener('click', () => { applyUiTheme(mode); hideSettingsMenu(); });
    settingsMenu.appendChild(it);
  }
  const label2 = el('div', 'settings-group-label', '终端配色');
  settingsMenu.appendChild(label2);
  for (const key of Object.keys(THEMES)) {
    const it = el('div', 'settings-item' + (key === state.currentTheme ? ' cur' : ''), THEMES[key].name);
    it.dataset.theme = key;
    it.style.setProperty('--dot', THEMES[key].background);
    it.addEventListener('click', () => {
      terminalController.applyTheme(key);
      hideSettingsMenu();
    });
    settingsMenu.appendChild(it);
  }
  // 底层 Shell：启动 claude 用的终端外壳（只影响之后新启动/恢复的会话）
  appendShellGroup();
  // 更新：手动"检查更新 / 更新到最新版"（GitHub Releases，v*-wails）
  appendUpdateGroup();
}

// 底层 Shell 选项单独构建（每次打开菜单都重新拉一次当前值）
async function appendShellGroup() {
  let shell = 'cmd';
  try { shell = await GetShell(); } catch (e) { /* 保持默认 cmd */ }
  const label3 = el('div', 'settings-group-label', '底层 Shell');
  settingsMenu.appendChild(label3);
  for (const [key, text] of [['cmd', 'cmd.exe（默认）'], ['pwsh', 'pwsh（PowerShell 7）']]) {
    const it = el('div', 'settings-item' + (key === shell ? ' cur' : ''), text);
    it.dataset.shell = key;
    it.title = key === 'pwsh'
      ? '需要已安装 PowerShell 7（pwsh 在 PATH 中）；claude 退出后停留在 pwsh 提示符'
      : 'Windows 自带；claude 退出后终端随之结束';
    it.addEventListener('click', async () => {
      // 选型时校验：pwsh 未安装则拒绝切换，避免"选完打不开"
      if (key === 'pwsh') {
        let ok = false;
        try { ok = await ShellInstalled('pwsh'); } catch (e) { /* 视为未安装 */ }
        if (!ok) {
          setStatus('未检测到 pwsh（PowerShell 7）：请先安装并确保 pwsh 在 PATH 中，或保持 cmd', 'warn');
          return;
        }
      }
      try {
        await SetShell(key);
      } catch (e) {
        setStatus('切换 Shell 失败: ' + e, 'warn');
        return;
      }
      hideSettingsMenu();
      setStatus('底层 Shell 已切换: ' + (key === 'pwsh' ? 'pwsh' : 'cmd') + '（新启动/恢复的会话生效）', 'ok');
    });
    settingsMenu.appendChild(it);
  }
  // 若已选择 pwsh 但当前系统检测不到：提示当前会以 cmd 兜底启动
  if (shell === 'pwsh') {
    let ok = true;
    try { ok = await ShellInstalled('pwsh'); } catch (e) { ok = false; }
    if (!ok) {
      settingsMenu.appendChild(el('div', 'settings-note', '⚠ 当前系统未检测到 pwsh，新启动的会话将以 cmd 兜底，装好后自动恢复 pwsh'));
    }
  }
}
settingsBtn.addEventListener('click', (ev) => {
  ev.stopPropagation();
  const willOpen = settingsMenu.style.display === 'none';
  hideSettingsMenu();
  if (!willOpen) return;
  buildSettingsMenu(); // 每次打开重建，确保 cur 标记为最新选择
  const r = settingsBtn.getBoundingClientRect();
  // 按钮位于窗口左下角，菜单向上展开
  settingsMenu.style.left = r.left + 'px';
  settingsMenu.style.bottom = (window.innerHeight - r.top + 4) + 'px';
  settingsMenu.style.display = 'block';
});
document.addEventListener('click', hideSettingsMenu);
window.addEventListener('blur', hideSettingsMenu);

// —— 更新：一键"更新到最新版"（GitHub Releases，v*-wails tag） ——
// 后端提供 GetVersion / CheckForUpdate / UpdateToLatest；
// 下载进度与阶段状态经 update:state / update:progress 事件推送。
const upd = { busy: false, pct: 0, phase: '' };
let updItem = null;

// 启动时把版本挂到"设置"按钮标题上（仅展示，不影响功能）
(async () => {
  let ver = '';
  try { ver = ' · v' + (await GetVersion()); } catch (e) { /* 忽略 */ }
  settingsBtn.title = '设置' + ver;
})();

// 后端阶段事件：下载阶段/失败/重启
window.runtime.EventsOn('update:state', (s) => {
  upd.phase = s || '';
  if (s === '下载失败' || s === '更新失败' || s === '检查失败') {
    upd.busy = false;
    setStatus('❌ ' + s, 'warn');
  } else if (s === '重启中') {
    agentController.showToast('✅ 更新完成，正在重启…');
  }
  renderUpdItem();
});
// 后端进度事件（0-100）
window.runtime.EventsOn('update:progress', (pct) => {
  upd.pct = Math.max(0, Math.min(100, pct | 0));
  renderUpdItem();
});

// 渲染当前更新项里的进度/状态（菜单每次打开会重建 updItem）
function renderUpdItem() {
  if (!updItem) return;
  updItem.querySelectorAll('.upd-col').forEach((c) => c.remove());
  let text = '';
  if (upd.busy && upd.phase === '下载中') {
    const col = el('div', 'upd-col');
    const bar = document.createElement('div');
    bar.className = 'upd-progress';
    const fill = document.createElement('div');
    fill.className = 'upd-progress-fill';
    fill.style.width = upd.pct + '%';
    bar.appendChild(fill);
    col.appendChild(bar);
    col.appendChild(el('div', 'upd-hint', '下载中 ' + upd.pct + '%'));
    updItem.appendChild(col);
  } else if (upd.phase && !upd.phase.includes('失败')) {
    const col = el('div', 'upd-col');
    col.appendChild(el('div', 'upd-hint', upd.phase));
    updItem.appendChild(col);
  }
}

// 设置菜单里追加"更新"分组
function appendUpdateGroup() {
  settingsMenu.appendChild(el('div', 'settings-group-label', '更新'));
  updItem = el('div', 'settings-item upd-run', '检查更新…');
  settingsMenu.appendChild(updItem);
  settingsMenu.appendChild(el('div', 'settings-note upd-note-ver', '点击检查 GitHub 上是否有新版（v*-wails）'));
  renderUpdItem();
  updItem.addEventListener('click', () => doCheckUpdate());
}

async function doCheckUpdate() {
  if (upd.busy) return;
  upd.busy = true;
  updItem.textContent = '正在检查…';
  let info = null;
  try {
    info = await CheckForUpdate();
  } catch (e) {
    upd.busy = false;
    setStatus('❌ ' + String((e && e.message) || e), 'warn');
    resetUpdMenu();
    return;
  }
  if (info && info.hasUpdate) {
    updItem.textContent = '发现新版本 ' + info.latest + ' → 点击更新并重启';
    updItem.classList.add('has-update');
    const note = settingsMenu.querySelector('.upd-note-ver');
    if (note) note.textContent = '当前 v' + (info.current || 'dev') + '；将下载并自动重启，运行中的会话进程会结束';
    setStatus('发现新版本 ' + info.latest + '（当前 v' + (info.current || 'dev') + '）', 'warn');
    const newItem = updItem.cloneNode(true); // 换绑点击为新动作
    updItem.replaceWith(newItem);
    updItem = newItem;
    updItem.addEventListener('click', () => doApplyUpdate(info));
  } else {
    upd.busy = false;
    const v = (info && info.current) || 'dev';
    setStatus('✅ 已是最新版本（v' + v + '）', 'ok');
    resetUpdMenu();
  }
}

function resetUpdMenu() {
  if (updItem) {
    updItem.textContent = '检查更新…';
    updItem.classList.remove('has-update', 'disabled');
  }
  const note = settingsMenu.querySelector('.upd-note-ver');
  if (note) note.textContent = '点击检查 GitHub 上是否有新版（v*-wails）';
  upd.phase = '';
  upd.pct = 0;
}

async function doApplyUpdate() {
  if (upd.busy) return;
  upd.busy = true;
  upd.pct = 0;
  upd.phase = '';
  updItem.textContent = '更新中…';
  updItem.classList.add('disabled');
  try {
    await UpdateToLatest();
    // 正常不会走到这里：后端启动新版后进程退出
    upd.busy = false;
    resetUpdMenu();
  } catch (e) {
    upd.busy = false;
    resetUpdMenu();
    setStatus('❌ 更新失败: ' + String((e && e.message) || e), 'warn');
  }
}
