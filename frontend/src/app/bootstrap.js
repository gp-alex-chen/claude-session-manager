import { createAppState } from '../state/app-state.js';
import { createTermOptions, THEMES } from '../themes/catalog.js';
import { createAgentController } from '../agents/controller.js';
import { createTerminalController } from '../terminal/controller.js';
import { createSessionController } from '../sessions/controller.js';
import { createSettingsController } from '../settings/controller.js';
import { createUpdateController } from '../updates/controller.js';
import { clampProgress } from '../utils.js';

const REQUIRED_IDS = [
  'terminal', 'status-bar', 'session-list', 'hidden-panel', 'hidden-count',
  'btn-hidden', 'btn-eye', 'btn-settings', 'settings-menu', 'settings-dialog',
  'settings-close', 'settings-nav', 'settings-tab-appearance',
  'settings-tab-terminal', 'settings-tab-update', 'settings-content',
  'settings-panel-appearance', 'settings-panel-terminal', 'settings-panel-update',
  'settings-version',
];

function requiredElement(documentRef, id) {
  const element = documentRef.getElementById(id);
  if (!element) throw new Error('Missing required DOM element: #' + id);
  return element;
}

function safeStorage(windowRef) {
  try { return windowRef?.localStorage || null; } catch (error) { return null; }
}

export function createApplication(deps) {
  const {
    documentRef,
    windowRef,
    runtime,
    backend,
    TerminalCtor,
    FitAddonCtor,
    themes = THEMES,
    createState = createAppState,
    createTermOptionsFn = createTermOptions,
    controllerFactories = {},
    onError,
  } = deps;
  const createAgent = controllerFactories.agent || createAgentController;
  const createTerminal = controllerFactories.terminal || createTerminalController;
  const createSession = controllerFactories.session || createSessionController;
  const createSettings = controllerFactories.settings || createSettingsController;
  const createUpdate = controllerFactories.update || createUpdateController;
  const nodes = Object.fromEntries(REQUIRED_IDS.map((id) => [id, requiredElement(documentRef, id)]));
  const state = createState();
  const termOptions = createTermOptionsFn();
  const setStatus = (message, className) => {
    nodes['status-bar'].textContent = '';
    nodes['status-bar'].appendChild(documentRef.createTextNode(String(message)));
    nodes['status-bar'].className = className || '';
  };
  const el = (tag, className, text) => {
    const element = documentRef.createElement(tag);
    if (className) element.className = className;
    if (text !== undefined) element.textContent = text;
    return element;
  };

  let sessionController;
  const agentController = createAgent({
    state,
    GetAgents: backend.GetAgents,
    DebugLog: backend.DebugLog,
    NotifyBeep: backend.NotifyBeep,
    setStatus,
    listRoot: nodes['session-list'],
    documentRef,
    refreshFoldState: () => sessionController?.refreshFoldState(),
  });
  const terminalController = createTerminal({
    state,
    backend: { TermWrite: backend.TermWrite, TermResize: backend.TermResize, TermKill: backend.TermKill },
    TerminalCtor,
    FitAddonCtor,
    termOptions,
    themes,
    setStatus,
    hostFactory: () => documentRef.createElement('div'),
    appendHost: (host) => nodes.terminal.appendChild(host),
    documentRef,
    storageRef: safeStorage(windowRef),
    onActivate: () => {
      sessionController?.syncActiveHighlight();
      agentController.renderUnreadMarks();
    },
  });
  sessionController = createSession({
    state,
    backend: {
      ListSessions: backend.ListSessions,
      ListHiddenSessions: backend.ListHiddenSessions,
      RenameSession: backend.RenameSession,
      DeleteSession: backend.DeleteSession,
      UnhideSession: backend.UnhideSession,
      StartSession: backend.StartSession,
      StartNew: backend.StartNew,
      GetOpenSessions: backend.GetOpenSessions,
    },
    terminalController,
    agentController,
    listRoot: nodes['session-list'],
    hiddenPanel: nodes['hidden-panel'],
    hiddenCount: nodes['hidden-count'],
    hiddenButton: nodes['btn-hidden'],
    eyeButton: nodes['btn-eye'],
    documentRef,
    windowRef,
    el,
    setStatus,
  });
  const updateController = createUpdate({
    backend: { CheckForUpdate: backend.CheckForUpdate, UpdateToLatest: backend.UpdateToLatest },
    el,
    setStatus,
    showToast: (message) => agentController.showToast(message),
    clampProgress,
  });
  const settingsController = createSettings({
    state,
    backend: {
      GetShell: backend.GetShell,
      ShellInstalled: backend.ShellInstalled,
      SetShell: backend.SetShell,
      GetVersion: backend.GetVersion,
    },
    terminalController,
    themes,
    settingsButton: nodes['btn-settings'],
    settingsMenu: nodes['settings-menu'],
    settingsDialog: nodes['settings-dialog'],
    settingsClose: nodes['settings-close'],
    settingsNav: nodes['settings-nav'],
    settingsVersion: nodes['settings-version'],
    categoryButtons: [
      nodes['settings-tab-appearance'],
      nodes['settings-tab-terminal'],
      nodes['settings-tab-update'],
    ],
    panels: {
      appearance: nodes['settings-panel-appearance'],
      terminal: nodes['settings-panel-terminal'],
      update: nodes['settings-panel-update'],
    },
    documentRef,
    windowRef,
    storage: safeStorage(windowRef),
    el,
    setStatus,
    updateController,
  });

  const controllers = { agent: agentController, terminal: terminalController, session: sessionController,
    settings: settingsController, update: updateController };
  const subscriptions = [];
  let resizeHandler = null;
  let started = false;
  let readyPromise = null;

  function reportError(error) {
    onError?.(error);
    if (!onError) setStatus('初始化失败: ' + ((error && error.message) || error), 'warn');
  }

  function initialize(controller) {
    try {
      return Promise.resolve(controller.initialize()).catch((error) => {
        reportError(error);
        return null;
      });
    } catch (error) {
      reportError(error);
      return Promise.resolve(null);
    }
  }

  function subscribe(name, handler) {
    const cancel = runtime.EventsOn(name, handler);
    subscriptions.push({ name, cancel });
  }

  function start() {
    if (started) return readyPromise;
    started = true;
    resizeHandler = () => terminalController.resizeActive();
    windowRef.addEventListener('resize', resizeHandler);
    subscribe('agents:update', (list) => agentController.applyAgents(list));
    subscribe('term:data', (token, b64) => terminalController.handleData(token, b64));
    subscribe('term:exit', (token) => terminalController.handleExit(token));
    subscribe('update:state', (phase) => updateController.handleState(phase));
    subscribe('update:progress', (progress) => updateController.handleProgress(progress));
    agentController.start();
    sessionController.start();
    settingsController.start();
    readyPromise = Promise.all([initialize(sessionController), initialize(settingsController)]);
    return readyPromise;
  }

  function stop() {
    if (!started) return;
    started = false;
    if (resizeHandler) windowRef.removeEventListener?.('resize', resizeHandler);
    resizeHandler = null;
    while (subscriptions.length) {
      const { name, cancel } = subscriptions.pop();
      if (typeof cancel === 'function') cancel();
      else runtime.EventsOff?.(name);
    }
    settingsController.stop();
    sessionController.stop();
    agentController.stop();
    readyPromise = null;
  }

  return {
    controllers,
    get ready() { return readyPromise; },
    start,
    state,
    stop,
  };
}
