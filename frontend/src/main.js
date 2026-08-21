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
import { createSettingsController } from './settings/controller.js';
import { createUpdateController } from './updates/controller.js';
import { clampProgress } from './utils.js';

// —— 多会话终端管理 ——
// 每个会话一个独立 xterm 实例；切换/关闭都在左侧列表操作，
// 后台会话继续运行并把输出写入自己（可能隐藏）的 Terminal。

const termStack = document.getElementById('terminal');
const statusEl = document.getElementById('status-bar');
const listEl = document.getElementById('session-list');

const state = createAppState();
const TERM_OPTS = createTermOptions();

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

const settingsButton = document.getElementById('btn-settings');
const settingsMenu = document.getElementById('settings-menu');
const updateController = createUpdateController({
  backend: { CheckForUpdate, UpdateToLatest },
  el,
  setStatus,
  showToast: (message) => agentController.showToast(message),
  clampProgress,
});
const settingsController = createSettingsController({
  state,
  backend: { GetShell, ShellInstalled, SetShell, GetVersion },
  terminalController,
  themes: THEMES,
  settingsButton,
  settingsMenu,
  documentRef: document,
  windowRef: window,
  storage: (() => {
    try { return window.localStorage; } catch (error) { return null; }
  })(),
  el,
  setStatus,
  updateController,
});

window.runtime.EventsOn('update:state', (phase) => updateController.handleState(phase));
window.runtime.EventsOn('update:progress', (progress) => updateController.handleProgress(progress));
settingsController.start();
void settingsController.initialize();
