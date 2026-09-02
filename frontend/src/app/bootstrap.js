import { createAppState } from '../state/app-state.js';
import { createTermOptions, THEMES } from '../themes/catalog.js';
import { createAgentController } from '../agents/controller.js';
import { createTerminalController } from '../terminal/controller.js';
import { createSessionController } from '../sessions/controller.js';
import { createSettingsController } from '../settings/controller.js';
import { createUpdateController } from '../updates/controller.js';
import { createUsageController } from '../usage/controller.js';
import { createUsageView } from '../usage/view.js';
import { clampProgress } from '../utils.js';
import { createPaneController } from '../panes/controller.js';

const REQUIRED_IDS = [
  'terminal', 'status-bar', 'status-message', 'project-bar', 'btn-add-project',
  'session-list', 'hidden-panel', 'hidden-count',
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
  const createPanes = controllerFactories.panes || createPaneController;
  const createUpdate = controllerFactories.update || createUpdateController;
  const createUsage = controllerFactories.usage || createUsageController;
  const nodes = Object.fromEntries(REQUIRED_IDS.map((id) => [id, requiredElement(documentRef, id)]));
  const state = createState();
  const termOptions = createTermOptionsFn();
  const setStatus = (message, className) => {
    nodes['status-message'].textContent = String(message);
    nodes['status-message'].className = className || '';
  };
  const el = (tag, className, text) => {
    const element = documentRef.createElement(tag);
    if (className) element.className = className;
    if (text !== undefined) element.textContent = text;
    return element;
  };
  let sessionController;
  let paneController;
  let usageController;
  let usageView;
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
    layoutManaged: true,
    hostFactory: () => documentRef.createElement('div'),
    appendHost: (host) => nodes.terminal.appendChild(host),
    documentRef,
    readClipboard: () => runtime.ClipboardGetText(),
    writeClipboard: (text) => runtime.ClipboardSetText(text),
    requestFrame: typeof windowRef?.requestAnimationFrame === 'function'
      ? (callback) => windowRef.requestAnimationFrame(callback)
      : undefined,
    storageRef: safeStorage(windowRef),
    onActivate: (token) => {
      usageController?.onActivate(token ?? state.activeToken);
      sessionController?.syncActiveHighlight();
      agentController.renderUnreadMarks();
    },
    onExit: (token) => {
      sessionController?.handleTerminalExit(token);
      paneController?.handleTerminalExit(token);
    },
    onDispose: (token) => {
      paneController?.removeSession(token);
      usageController?.removeToken?.(token);
    },
  });
  paneController = createPanes({
    state,
    terminalController,
    terminalRoot: nodes.terminal,
    statusBar: nodes['status-bar'],
    documentRef,
    storageRef: safeStorage(windowRef),
    el,
    setStatus,
    onFocus: (token) => {
      usageController?.onActivate(token);
      sessionController?.syncActiveHighlight();
      agentController.renderUnreadMarks();
    },
    onChange: () => {
      usageView?.render(state);
      void usageController?.refreshVisible?.({ force: false });
    },
  });
  usageView = createUsageView({
    surfaces: paneController.view.paneSurfaces(),
    documentRef,
    windowRef,
  });
  usageController = createUsage({
    state,
    GetUsageSummary: backend.GetUsageSummary,
    view: usageView,
    surfaces: paneController.view.paneSurfaces(),
    getVisibleAssignments: () => {
      const visible = new Set(paneController?.getVisiblePaneIds?.() || []);
      return state.panes.filter((pane) => visible.has(pane.id) && pane.token);
    },
    render: (nextState) => {
      usageView.render(nextState);
      sessionController?.refreshUsageLabels();
    },
  });
  usageView.render(state);
  sessionController = createSession({
    state,
    backend: {
      ListSessions: backend.ListSessions,
      ListHiddenSessions: backend.ListHiddenSessions,
      RenameSession: backend.RenameSession,
      DeleteSession: backend.DeleteSession,
      UnhideSession: backend.UnhideSession,
      AdoptSession: backend.AdoptSession,
      StartSession: backend.StartSession,
      StartNew: backend.StartNew,
      OpenFolder: backend.OpenFolder,
      GetOpenSessions: backend.GetOpenSessions,
      ListProjects: backend.ListProjects,
      ListProjectFavorites: backend.ListProjectFavorites,
      SetProjectFavorite: backend.SetProjectFavorite,
      ChooseProjectDir: backend.ChooseProjectDir,
      AddProject: backend.AddProject,
    },
    terminalController,
    paneController,
    agentController,
    listRoot: nodes['session-list'],
    addProjectButton: nodes['btn-add-project'],
    hiddenPanel: nodes['hidden-panel'],
    hiddenCount: nodes['hidden-count'],
    hiddenButton: nodes['btn-hidden'],
    eyeButton: nodes['btn-eye'],
    documentRef,
    windowRef,
    el,
    setStatus,
    onProjects: usageController.prefetchProjects,
    onPair: (pendingItem) => {
      usageController.refreshToken?.(pendingItem.token, { force: true });
    },
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

  const controllers = { agent: agentController, terminal: terminalController, panes: paneController, session: sessionController,
    settings: settingsController, update: updateController, usage: usageController };
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
    resizeHandler = () => {
      if (typeof paneController.resizeVisible === 'function') paneController.resizeVisible();
      else terminalController.resizeActive();
    };
    windowRef.addEventListener('resize', resizeHandler);
    subscribe('agents:update', (list) => agentController.applyAgents(list));
    subscribe('term:data', (token, b64) => terminalController.handleData(token, b64));
    subscribe('term:exit', (token) => terminalController.handleExit(token));
    subscribe('update:state', (phase) => updateController.handleState(phase));
    subscribe('update:progress', (progress) => updateController.handleProgress(progress));
    agentController.start();
    paneController.start();
    usageController.start();
    sessionController.start();
    settingsController.start();
    readyPromise = initialize(paneController).then(() => Promise.all([
      initialize(sessionController), initialize(settingsController),
    ]));
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
    paneController.stop();
    usageController.stop();
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
