import { createAppState } from '../state/app-state.js';
import { createTermOptions, THEMES } from '../themes/catalog.js';
import { createAgentController } from '../agents/controller.js';
import { createTerminalController } from '../terminal/controller.js';
import { createSessionController } from '../sessions/controller.js';
import { createSettingsController } from '../settings/controller.js';
import { createPreferenceStorage } from '../settings/storage.js';
import { createUpdateController } from '../updates/controller.js';
import { createUsageController } from '../usage/controller.js';
import { createUsageView } from '../usage/view.js';
import { clampProgress } from '../utils.js';
import { createPaneController } from '../panes/controller.js';
import { createWorkspaceController } from '../workspace/controller.js';
import { createQuotaController } from '../quota/controller.js';
import { createQuotaView } from '../quota/view.js';
import { createCompletionController } from '../notifications/controller.js';

const REQUIRED_IDS = [
  'terminal', 'status-bar', 'status-message', 'project-bar', 'btn-add-project',
  'session-list', 'hidden-panel', 'hidden-count', 'provider-filters',
  'btn-hidden', 'btn-eye', 'btn-settings', 'update-notice', 'settings-menu', 'settings-dialog',
  'settings-close', 'settings-nav', 'settings-tab-appearance',
  'settings-tab-terminal', 'settings-tab-update', 'settings-content',
  'settings-panel-appearance', 'settings-panel-terminal', 'settings-panel-update',
  'settings-version',
  'app', 'sidebar', 'sidebar-divider', 'workspace-toolbar', 'workspace-project',
  'workspace-directory', 'workspace-summary', 'btn-sidebar-toggle', 'account-quota',
  'session-search', 'btn-opened-only', 'archive-overlay', 'archive-dialog', 'archive-search', 'btn-close-archive',
];

function requiredElement(documentRef, id) {
  const element = documentRef.getElementById(id);
  if (!element) throw new Error('Missing required DOM element: #' + id);
  return element;
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
  const completionController = createCompletionController({ state, documentRef, windowRef,
    SetCompletionBadge: backend.SetCompletionBadge, onRead: () => agentController.renderUnreadMarks() });
  const termOptions = createTermOptionsFn();
  let workspaceController;
  const setStatus = (message, className) => {
    if (workspaceController) return workspaceController.setStatus(message, className);
    nodes['status-message'].textContent = String(message);
    nodes['status-message'].className = className || '';
  };
  const el = (tag, className, text) => {
    const element = documentRef.createElement(tag);
    if (className) element.className = className;
    if (text !== undefined) element.textContent = text;
    return element;
  };
  const preferences = createPreferenceStorage({
    backend,
    onError: (error) => setStatus('保存或读取应用设置失败: ' + error, 'warn'),
  });
  let sessionController;
  let paneController;
  let usageController;
  let usageView;
  let quotaController;
  workspaceController = createWorkspaceController({ state, nodes, storage: preferences, documentRef,
    onResize: () => paneController?.resizeVisible?.() });
  const agentController = createAgent({
    state,
    GetAgents: backend.GetAgents,
    DebugLog: backend.DebugLog,
    NotifyBeep: backend.NotifyBeep,
    setStatus,
    listRoot: nodes['session-list'],
    documentRef,
    refreshFoldState: () => sessionController?.refreshFoldState(),
    onAgents: (list) => sessionController?.handleAgentIdentities?.(list),
    onTaskCompleted: completionController.complete,
    onUnreadChange: completionController.sync,
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
    storageRef: preferences,
    onActivate: (token) => {
      completionController.activate(token ?? state.activeToken);
      usageController?.onActivate(token ?? state.activeToken);
      sessionController?.syncActiveHighlight();
      agentController.renderUnreadMarks();
      workspaceController.refreshContext();
      quotaController?.sync();
    },
    onBeforeClose: completionController.dismiss,
    onExit: (token) => {
      sessionController?.handleTerminalExit(token);
      paneController?.handleTerminalExit(token);
      sessionController?.refreshSessionVisibility?.();
      workspaceController.refreshContext();
      quotaController?.sync();
    },
    onDispose: (token) => {
      completionController.dismiss(token);
      paneController?.removeSession(token);
      usageController?.removeToken?.(token);
      sessionController?.refreshSessionVisibility?.();
      workspaceController.refreshContext();
      quotaController?.sync();
    },
  });
  paneController = createPanes({
    state,
    terminalController,
    terminalRoot: nodes.terminal,
    statusBar: nodes['workspace-toolbar'],
    documentRef,
    storageRef: preferences,
    el,
    setStatus,
    onFocus: (token) => {
      completionController.activate(token);
      usageController?.onActivate(token);
      sessionController?.syncActiveHighlight();
      agentController.renderUnreadMarks();
      workspaceController.refreshContext();
      quotaController?.sync();
    },
    onChange: () => {
      usageView?.render(state);
      void usageController?.refreshVisible?.({ force: false });
      workspaceController.refreshContext();
      quotaController?.sync();
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
  const quotaView = createQuotaView({ root: nodes['account-quota'], documentRef,
    onRefresh: () => quotaController?.refresh(true) });
  quotaController = createQuotaController({ state, view: quotaView, GetAccountQuota: backend.GetAccountQuota });
  sessionController = createSession({
    state,
    backend: {
      ListSessions: backend.ListSessions,
      ListHiddenSessions: backend.ListHiddenSessions,
      ListSessionsForProviders: backend.ListSessionsForProviders,
      ListHiddenSessionsForProviders: backend.ListHiddenSessionsForProviders,
      RenameSession: backend.RenameSession,
      DeleteSession: backend.DeleteSession,
      UnhideSession: backend.UnhideSession,
      AdoptSession: backend.AdoptSession,
      StartSession: backend.StartSession,
      StartNew: backend.StartNew,
      StartNewAgent: backend.StartNewAgent,
      ListAgentProviders: backend.ListAgentProviders,
      OpenFolder: backend.OpenFolder,
      GetOpenSessions: backend.GetOpenSessions,
      GetOpenSessionRefs: backend.GetOpenSessionRefs,
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
    filterRoot: nodes['provider-filters'],
    storageRef: preferences,
    addProjectButton: nodes['btn-add-project'],
    hiddenPanel: nodes['hidden-panel'],
    hiddenCount: nodes['hidden-count'],
    hiddenButton: nodes['btn-hidden'],
    searchInput: nodes['session-search'],
    openedButton: nodes['btn-opened-only'],
    archiveOverlay: nodes['archive-overlay'],
    archiveDialog: nodes['archive-dialog'],
    archiveSearch: nodes['archive-search'],
    archiveClose: nodes['btn-close-archive'],
    workspaceRoot: nodes.app,
    eyeButton: nodes['btn-eye'],
    documentRef,
    windowRef,
    el,
    setStatus,
    onProjects: usageController.prefetchProjects,
    onVisibilityChange: workspaceController.refreshContext,
    onPair: (pendingItem) => {
      completionController.activate(pendingItem.token);
      usageController.refreshToken?.(pendingItem.token, { force: true });
      quotaController.sync();
    },
  });
  const updateController = createUpdate({
    backend: { CheckForUpdate: backend.CheckForUpdate, UpdateToLatest: backend.UpdateToLatest },
    el,
    setStatus,
    showToast: (message) => agentController.showToast(message),
    clampProgress,
    noticeNode: nodes['update-notice'],
    storage: preferences,
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
    storage: preferences,
    el,
    setStatus,
    updateController,
  });

  const controllers = { agent: agentController, terminal: terminalController, panes: paneController, session: sessionController,
    settings: settingsController, update: updateController, usage: usageController, workspace: workspaceController, quota: quotaController,
    completion: completionController };
  const subscriptions = [];
  let resizeHandler = null;
  let started = false;
  let generation = 0;
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
    const startupGeneration = ++generation;
    const current = () => started && generation === startupGeneration;
    nodes.terminal.inert = true;
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
    readyPromise = preferences.initialize().then(async () => {
      if (!current()) return;
      workspaceController.initialize();
      workspaceController.start();
      completionController.start();
      agentController.start();
      paneController.start();
      usageController.start();
      quotaController.start();
      sessionController.start();
      settingsController.start();
      updateController.start?.();
      await initialize(paneController);
      if (!current()) return;
      await initialize(settingsController);
      if (current()) await initialize(sessionController);
      if (current()) nodes.terminal.inert = false;
    });
    return readyPromise;
  }

  function stop() {
    if (!started) return;
    started = false;
    generation += 1;
    nodes.terminal.inert = false;
    if (resizeHandler) windowRef.removeEventListener?.('resize', resizeHandler);
    resizeHandler = null;
    while (subscriptions.length) {
      const { name, cancel } = subscriptions.pop();
      if (typeof cancel === 'function') cancel();
      else runtime.EventsOff?.(name);
    }
    settingsController.stop();
    completionController.stop();
    quotaController.stop();
    workspaceController.stop();
    updateController.stop?.();
    sessionController.stop();
    paneController.stop();
    usageController.stop();
    agentController.stop();
    readyPromise = null;
  }

  return {
    controllers,
    flushPreferences: preferences.flush,
    get ready() { return readyPromise; },
    start,
    state,
    stop,
  };
}
