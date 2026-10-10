import test from 'node:test';
import assert from 'node:assert/strict';

import { createAppState } from '../src/state/app-state.js';
import { createSessionController } from '../src/sessions/controller.js';
import { listSig, pairPendingSessions } from '../src/sessions/pairing.js';
import { renderSessionList, renderHiddenSessions } from '../src/sessions/view.js';

class FakeClassList {
  constructor() { this.values = new Set(); }
  add(...names) { names.forEach((name) => this.values.add(name)); }
  remove(...names) { names.forEach((name) => this.values.delete(name)); }
  contains(name) { return this.values.has(name); }
  toggle(name, force) {
    if (force === undefined ? !this.values.delete(name) : force) this.values.add(name);
    else this.values.delete(name);
  }
}

class FakeNode {
  constructor() {
    this.children = [];
    this.parentNode = null;
    this.dataset = {};
    this.style = { setProperty() {} };
    this.classList = new FakeClassList();
    this.listeners = new Map();
    this.listenerCounts = new Map();
    this._innerHTML = '';
    this.textContent = '';
    this.attributes = new Map();
  }
  get innerHTML() { return this._innerHTML; }
  set innerHTML(value) {
    this._innerHTML = String(value);
    this.children = [];
  }
  append(...children) {
    for (const child of children) {
      child.parentNode = this;
      this.children.push(child);
    }
  }
  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    return child;
  }
  addEventListener(name, callback) {
    this.listeners.set(name, callback);
    this.listenerCounts.set(name, (this.listenerCounts.get(name) || 0) + 1);
  }
  dispatchEvent(event) {
    const result = this.listeners.get(event.type)?.(event);
    if (event.bubbles && !event.cancelBubble) this.parentNode?.dispatchEvent(event);
    return result;
  }
  click() {
    const event = {
      type: 'click',
      bubbles: true,
      cancelBubble: false,
      stopPropagation() { this.cancelBubble = true; },
    };
    return this.dispatchEvent(event);
  }
  querySelectorAll() { return []; }
  querySelector() { return null; }
  remove() { this.removed = true; }
  setAttribute(name, value) { this.attributes.set(name, String(value)); }
}

function makeFixture(options = {}) {
  const state = createAppState();
  const listRoot = new FakeNode();
  const addProjectButton = new FakeNode();
  let renderCount = 0;
  Object.defineProperty(listRoot, 'innerHTML', {
    get() { return ''; },
    set(value) {
      listRoot._innerHTML = String(value);
      listRoot.children = [];
      renderCount += 1;
    },
  });
  const hiddenPanel = new FakeNode();
  const hiddenCount = new FakeNode();
  const hiddenButton = new FakeNode();
  const eyeButton = new FakeNode();
  const filterRoot = new FakeNode();
  const searchInput = new FakeNode();
  const openedButton = new FakeNode();
  const archiveOverlay = new FakeNode();
  const archiveDialog = new FakeNode();
  const archiveSearch = new FakeNode();
  const archiveClose = new FakeNode();
  const workspaceRoot = new FakeNode();
  const documentListeners = new Map();
  const documentRef = {
    body: new FakeNode(),
    createElement: () => new FakeNode(),
    addEventListener: (name, callback) => documentListeners.set(name, callback),
    removeEventListener: (name, callback) => { if (documentListeners.get(name) === callback) documentListeners.delete(name); },
  };
  for (const element of [archiveSearch, archiveClose, hiddenButton]) element.focus = () => { documentRef.activeElement = element; };
  archiveDialog.querySelectorAll = () => [archiveClose, archiveSearch];
  const windowRef = {
    innerWidth: 1000,
    innerHeight: 800,
    prompt: options.prompt || (() => 'Renamed'),
    confirm: () => options.confirmResult ?? true,
    addEventListener() {},
  };
  const terminals = new Map();
  const terminalController = {
    openTab(token, name) {
      const terminal = { token, labelText: name, exited: false };
      terminals.set(token, terminal);
      state.terminals.set(token, terminal);
      return terminal;
    },
    activate(token) { terminalController.activations.push(token); },
    activations: [],
    disposeSession(token) {
      terminals.delete(token);
      state.terminals.delete(token);
      terminalController.disposed.push(token);
    },
    disposed: [],
    closeTab(token) {
      terminalController.closeChecks?.(token);
      terminals.delete(token);
      state.terminals.delete(token);
      terminalController.closed.push(token);
    },
    closed: [],
  };
  const paneController = options.paneController || null;
  const agentController = {
    classifyAgent: () => 'idle',
    refreshAgents: async () => {},
    renderUnreadMarks: () => {},
  };
  const statuses = [];
  let listIndex = 0;
  const listCalls = [];
  const openCalls = [];
  const projectCalls = [];
  const projectAddCalls = [];
  const chooserCalls = [];
  const folderCalls = [];
  const favoriteCalls = [];
  const adoptionCalls = [];
  let projectIndex = 0;
  let favoriteIndex = 0;
  const listResults = options.listResults || [[]];
  const projectResults = options.projectResults || [[]];
  const favoriteResults = options.favoriteResults || [[]];
  const backend = {
    ListSessions: async () => {
      listCalls.push(true);
      if (options.listWait) return options.listWait;
      const result = listResults[Math.min(listIndex++, listResults.length - 1)];
      if (result instanceof Error) throw result;
      return result;
    },
    ListHiddenSessions: options.ListHiddenSessions || (async () => []),
    ListSessionsForProviders: options.ListSessionsForProviders,
    ListHiddenSessionsForProviders: options.ListHiddenSessionsForProviders,
    RenameSession: options.RenameSession || (async () => {}),
    DeleteSession: options.DeleteSession || (async () => {}),
    UnhideSession: async () => {},
    StartSession: options.StartSession || (async () => {}),
    StartNew: options.StartNew || (async () => 'new-1'),
    StartNewAgent: options.StartNewAgent,
    ListAgentProviders: options.ListAgentProviders,
    AdoptSession: options.AdoptSession || (async (token, realId) => {
      adoptionCalls.push([token, realId]);
    }),
    GetOpenSessions: options.GetOpenSessions || (async () => { openCalls.push(true); return []; }),
    GetOpenSessionRefs: options.GetOpenSessionRefs,
    ListProjects: options.ListProjects || (async () => {
      const result = projectResults[Math.min(projectIndex++, projectResults.length - 1)];
      if (result instanceof Error) throw result;
      return result;
    }),
    ChooseProjectDir: options.ChooseProjectDir || (async () => {
      chooserCalls.push(true);
      return options.chosenDir || '';
    }),
    AddProject: options.AddProject || (async (dir) => { projectAddCalls.push(dir); }),
    OpenFolder: options.OpenFolder || (async (dir) => { folderCalls.push(dir); }),
    ListProjectFavorites: options.ListProjectFavorites || (async () => {
      const result = favoriteResults[Math.min(favoriteIndex++, favoriteResults.length - 1)];
      if (result instanceof Error) throw result;
      return result;
    }),
    SetProjectFavorite: options.SetProjectFavorite || (async (dir, favorite) => {
      favoriteCalls.push([dir, favorite]);
    }),
  };
  const intervals = [];
  const cleared = [];
  const controller = createSessionController({
    state,
    backend,
    terminalController,
    paneController,
    agentController,
    listRoot,
    filterRoot,
    storageRef: options.storageRef,
    addProjectButton,
    hiddenPanel,
    hiddenCount,
    hiddenButton,
    searchInput, openedButton, archiveOverlay, archiveDialog, archiveSearch, archiveClose, workspaceRoot,
    eyeButton,
    documentRef,
    windowRef,
    el: (tag, cls, text) => {
      const node = new FakeNode();
      node.className = cls;
      node.textContent = text || '';
      return node;
    },
    setStatus: (message, kind) => statuses.push({ message, kind }),
    onProjects: (list) => projectCalls.push(list),
    setIntervalFn: (callback, delay) => {
      const timer = { callback, delay };
      intervals.push(timer);
      return timer;
    },
    clearIntervalFn: (timer) => cleared.push(timer),
  });
  return {
    state, controller, backend, terminals, statuses, intervals, cleared, listCalls,
    listRoot, addProjectButton, terminalController, projectCalls, projectAddCalls,
    chooserCalls, openCalls,
    documentRef, folderCalls,
    favoriteCalls,
    adoptionCalls,
    filterRoot, hiddenCount, hiddenButton, hiddenPanel,
    paneController,
    searchInput, openedButton, archiveOverlay, archiveSearch, archiveClose, workspaceRoot, documentListeners,
    get renderCount() { return renderCount; },
  };
}

const session = (id, dir = 'work', name = id, time = 'today') => ({ id, dir, name, time });

const filterAgents = [{ id: 'claude', name: 'Claude' }, { id: 'codex', name: 'Codex' }];
const filterClaude = { ...session('claude-old', 'work', 'Claude old'), provider: 'claude' };
const filterCodex = { ...session('codex:01900000-0000-7000-8000-000000000001', 'work', 'Codex old'), provider: 'codex' };

test('native Codex identity replaces temporary row and follows authoritative history without another terminal', async () => {
  const f = makeFilterFixture({ StartNewAgent: async () => 'new-codex-native', read: async ids => ({ sessions: [], succeededProviders: ids, warnings: [] }) });
  await f.controller.initialize();
  await f.controller.startNew('work', 'codex');
  const snapshot = [{ provider: 'codex', runtimeToken: 'new-codex-native', sessionId: filterCodex.id, restorable: true }];
  f.controller.handleAgentIdentities(snapshot);
  assert.equal(f.state.pendingNew.length, 0);
  assert.equal(f.state.newToReal.get('new-codex-native'), filterCodex.id);
  assert.deepEqual(visibleIDs(f), [filterCodex.id]);
  assert.doesNotMatch(f.listRoot.children[0].children.at(-1).textContent || '', /重启不恢复/);
  f.controller.renderSessions([filterCodex]);
  assert.deepEqual(visibleIDs(f), [filterCodex.id]);
  assert.equal(f.state.terminals.get('new-codex-native').labelText, filterCodex.name);
  await f.controller.openFromList(filterCodex);
  assert.equal(f.state.terminals.size, 1);
  assert.equal(f.adoptionCalls.length, 0, 'Codex binding must not use Claude directory adoption');
  f.controller.closeRealSession(filterCodex.id);
  f.controller.handleAgentIdentities(snapshot);
  assert.equal(f.state.terminals.size, 0, 'late identity cannot reopen a closed terminal');
});

test('native Codex remains pending until saved and /new removes its old formal identity', async () => {
  const f = makeFilterFixture({StartNewAgent:async()=> 'new-codex-switch'});
  await f.controller.initialize();
  await f.controller.startNew('work','codex');
  const info = {provider:'codex',runtimeToken:'new-codex-switch',sessionId:filterCodex.id};
  f.controller.handleAgentIdentities([info]);
  assert.equal(f.state.newToReal.size,0);
  assert.equal(f.state.pendingNew[0].identityKnown,true);
  f.controller.handleAgentIdentities([{...info,restorable:true}]);
  assert.equal(f.state.newToReal.get(info.runtimeToken),filterCodex.id);
  const next = {...info,sessionId:'codex:01900000-0000-7000-8000-000000000002'};
  f.controller.handleAgentIdentities([next]);
  assert.equal(f.state.newToReal.size,0);
  assert.equal(f.state.realToNew.size,0);
  assert.equal(f.state.pendingNew.length,1);
  f.controller.handleAgentIdentities([{...next,restorable:true,name:'New native title'}]);
  assert.equal(f.state.pendingNew.length,0);
  assert.equal(f.state.newToReal.get(info.runtimeToken),next.sessionId);
  assert.equal(f.state.terminals.get(info.runtimeToken).labelText,'New native title');
  assert.equal(f.state.terminals.size,1);
});

test('a restored Codex terminal switched to another thread cannot be opened or closed as the old history', async () => {
  const f = makeFilterFixture({GetOpenSessionRefs:async()=>[{id:filterCodex.id,provider:'codex'}]});
  await f.controller.initialize();
  f.controller.handleAgentIdentities([{provider:'codex',runtimeToken:filterCodex.id,sessionId:'codex:01900000-0000-7000-8000-000000000002',restorable:true}]);
  assert.equal(await f.controller.openFromList(filterCodex),false);
  f.controller.closeRealSession(filterCodex.id);
  assert.equal(f.state.terminals.has(filterCodex.id),true);
  assert.match(f.statuses.at(-1).message,/已切换/);
  const label = f.state.terminals.get(filterCodex.id).labelText;
  await f.controller.renameSession(filterCodex);
  assert.equal(f.state.terminals.get(filterCodex.id).labelText,label,'renaming old history must not rename the switched terminal');
  f.state.selectedProviders = new Set(['claude']);
  f.controller.refreshSessionVisibility();
  assert.deepEqual(visibleIDs(f),[filterClaude.id,'codex:01900000-0000-7000-8000-000000000002']);
});

function makeFilterFixture(options = {}) {
  const saved = options.saved || new Map();
  const queries = [];
  const hiddenQueries = [];
  const fixture = makeFixture({
    ...options,
    storageRef: { getItem: (key) => saved.get(key), setItem: (key, value) => saved.set(key, value) },
    ListAgentProviders: options.ListAgentProviders || (async () => filterAgents),
    GetOpenSessionRefs: options.GetOpenSessionRefs || (async () => []),
    ListSessionsForProviders: async (ids) => {
      queries.push(ids);
      if (options.read) return options.read(ids, queries.length);
      return { sessions: [filterClaude, filterCodex].filter((row) => ids.includes(row.provider)), succeededProviders: ids, warnings: [] };
    },
    ListHiddenSessionsForProviders: async (ids) => {
      hiddenQueries.push(ids);
      if (options.readHidden) return options.readHidden(ids, hiddenQueries.length);
      return { sessions: [], succeededProviders: ids, warnings: [] };
    },
  });
  return { ...fixture, saved, queries, hiddenQueries };
}

function visibleIDs(fixture) {
  return fixture.listRoot.children.flatMap((group) => (group.children[1]?.children || []).map((row) => row.dataset.id));
}

test('search expands matches without changing provider reads, history names or pane choices', async () => {
  let choices=[];
  const f=makeFilterFixture({paneController:{setSessionOptions:list=>{choices=list;}}});
  await f.controller.initialize();
  const before=f.queries.length;
  f.searchInput.value='CLAUDE';f.searchInput.dispatchEvent({type:'input'});
  assert.deepEqual(visibleIDs(f),[filterClaude.id]);
  assert.equal(f.listRoot.children[0].classList.contains('collapsed'),false);
  assert.equal(f.listRoot.children[0].children[1].children[0].classList.contains('fold-hidden'),false);
  assert.equal(f.state.collapsedDirs.has('work'),true);
  assert.deepEqual(choices.map(row=>row.id),[filterClaude.id,filterCodex.id]);
  assert.equal(f.state.sessionNames.get(filterCodex.id),filterCodex.name);
  f.searchInput.value='WORK';f.searchInput.dispatchEvent({type:'input'});
  assert.deepEqual(visibleIDs(f),[filterClaude.id,filterCodex.id]);
  f.searchInput.value='no match';f.searchInput.dispatchEvent({type:'input'});
  assert.deepEqual(visibleIDs(f),[]);
  assert.equal(f.listRoot.children[0].textContent,'没有匹配的项目或会话');
  assert.equal(f.queries.length,before);
});

test('opened filter combines with search and follows live, exited and temporary terminals', async () => {
  const f=makeFilterFixture();await f.controller.initialize();
  f.state.terminals.set(filterClaude.id,{exited:false});
  f.state.terminals.set(filterCodex.id,{exited:true});
  f.state.terminals.set('new-codex-test',{exited:false,provider:'codex'});
  f.state.pendingNew.push({token:'new-codex-test',dir:'work',provider:'codex',name:'Temporary'});
  f.openedButton.click();
  assert.deepEqual(visibleIDs(f),[filterClaude.id,'new-codex-test']);
  assert.equal(f.openedButton.textContent,'已打开 2');
  f.searchInput.value='Temporary';f.searchInput.dispatchEvent({type:'input'});
  assert.deepEqual(visibleIDs(f),['new-codex-test']);
  f.controller.closeRealSession('new-codex-test');
  assert.deepEqual(visibleIDs(f),[]);
  assert.equal(f.openedButton.textContent,'已打开 1');
  assert.equal(f.state.terminals.has(filterClaude.id),true);
});

test('archive dialog searches locally, restores the original identity, and releases focus and inert state', async () => {
  let archived=[filterClaude,filterCodex];
  const f=makeFilterFixture({readHidden:async ids=>({sessions:archived.filter(row=>ids.includes(row.provider)),succeededProviders:ids,warnings:[]})});
  const restored=[];
  f.backend.UnhideSession=async id=>{restored.push(id);archived=archived.filter(row=>row.id!==id);};
  f.controller.start();await f.controller.initialize();
  const navigation=visibleIDs(f);
  f.hiddenButton.click();await f.controller.refreshHidden(false);
  assert.equal(f.archiveOverlay.hidden,false);
  assert.equal(f.workspaceRoot.inert,true);
  assert.equal(f.documentRef.activeElement,f.archiveSearch);
  f.archiveSearch.value='Codex';f.archiveSearch.dispatchEvent({type:'input'});
  assert.equal(f.hiddenPanel.children.length,1);
  assert.equal(f.hiddenCount.textContent,' · 2');
  assert.deepEqual(visibleIDs(f),navigation);
  await f.hiddenPanel.children[0].children.at(-1).click();
  await f.controller.refreshHidden(false);
  assert.deepEqual(restored,[filterCodex.id]);
  assert.equal(f.hiddenPanel.children[0].textContent,'没有匹配的归档会话');
  f.documentListeners.get('keydown')({key:'Tab',shiftKey:false,preventDefault(){}});
  assert.equal(f.documentRef.activeElement,f.archiveClose);
  f.documentListeners.get('keydown')({key:'Escape',preventDefault(){}});
  assert.equal(f.archiveOverlay.hidden,true);
  assert.equal(f.workspaceRoot.inert,false);
  assert.equal(f.documentRef.activeElement,f.hiddenButton);
  f.controller.stop();assert.equal(f.documentListeners.has('keydown'),false);
});

test('navigation search never prevents restoring a filtered-out provider at startup', async () => {
  const f=makeFilterFixture({saved:new Map([['session-provider-filter','["claude"]']]),
    GetOpenSessionRefs:async()=>[{id:filterCodex.id,provider:'codex'}]});
  f.searchInput.value='no match';f.searchInput.dispatchEvent({type:'input'});
  await f.controller.initialize();
  assert.equal(f.state.terminals.has(filterCodex.id),true);
  assert.deepEqual(visibleIDs(f),[]);
  assert.equal(f.state.sessionNames.get(filterCodex.id),filterCodex.name);
});

test('provider buttons default to both, persist filtering, and protect the last selection', async () => {
  const fixture = makeFilterFixture();
  await fixture.controller.initialize();
  assert.deepEqual([...fixture.state.selectedProviders], ['claude', 'codex']);
  assert.deepEqual(fixture.filterRoot.children.map((button) => button.attributes.get('aria-pressed')), ['true', 'true']);
  await fixture.filterRoot.children[1].click();
  assert.deepEqual(visibleIDs(fixture), [filterClaude.id]);
  assert.deepEqual(fixture.queries.at(-1), ['claude']);
  assert.equal(fixture.saved.get('session-provider-filter'), '["claude"]');
  assert.equal(fixture.filterRoot.children[0].disabled, true);
  const calls = fixture.queries.length;
  await fixture.controller.toggleProvider('claude');
  assert.equal(fixture.queries.length, calls);
  const restarted = makeFilterFixture({ saved: fixture.saved });
  await restarted.controller.initialize();
  assert.deepEqual(restarted.queries[0], ['claude']);
  assert.deepEqual(visibleIDs(restarted), [filterClaude.id]);
});

test('invalid and unavailable saved sources safely restore both buttons', async () => {
  for (const value of ['{', '[]', '["unknown"]']) {
    const fixture = makeFilterFixture({ saved: new Map([['session-provider-filter', value]]) });
    await fixture.controller.initialize();
    assert.deepEqual(fixture.queries[0], ['claude', 'codex']);
  }
});

test('filtered-out restored Codex stays visible until its tab closes', async () => {
  const fixture = makeFilterFixture({
    saved: new Map([['session-provider-filter', '["claude"]']]),
    GetOpenSessionRefs: async () => [{ id: filterCodex.id, provider: 'codex' }],
  });
  await fixture.controller.initialize();
  assert.deepEqual(fixture.queries[0], ['claude', 'codex']);
  assert.ok(fixture.state.terminals.has(filterCodex.id));
  assert.ok(visibleIDs(fixture).includes(filterCodex.id));
  fixture.controller.closeRealSession(filterCodex.id);
  assert.deepEqual(visibleIDs(fixture), [filterClaude.id]);
  await fixture.controller.loadSessions();
  assert.deepEqual(fixture.queries.at(-1), ['claude']);
});

test('unselected temporary Codex stays visible without querying its history', async () => {
  const fixture = makeFilterFixture({
    saved: new Map([['session-provider-filter', '["claude"]']]),
    StartNewAgent: async () => 'new-codex-fixture',
  });
  await fixture.controller.initialize();
  await fixture.controller.startNew('work', 'codex');
  await fixture.controller.loadSessions();
  assert.deepEqual(fixture.queries.at(-1), ['claude']);
  assert.ok(visibleIDs(fixture).includes('new-codex-fixture'));
});

test('Codex-only startup preflights Claude before launch and adopts only genuinely new history', async () => {
  let launched = false;
  let newHistory = false;
  const oldTwo = { ...filterClaude, id: 'claude-old-two' };
  const newRow = { ...filterClaude, id: 'claude-new', name: 'New Claude' };
  const fixture = makeFilterFixture({
    saved: new Map([['session-provider-filter', '["codex"]']]),
    read: async (ids) => ({
      sessions: [...(ids.includes('codex') ? [filterCodex] : []),
        ...(ids.includes('claude') ? [filterClaude, oldTwo, ...(newHistory ? [newRow] : [])] : [])],
      succeededProviders: ids, warnings: [],
    }),
    StartNew: async () => { launched = true; return 'new-claude'; },
  });
  await fixture.controller.initialize();
  assert.deepEqual(fixture.queries[0], ['codex']);
  await fixture.controller.startNew('work', 'claude');
  assert.equal(launched, true);
  assert.deepEqual(fixture.queries[1], ['claude']);
  assert.deepEqual(fixture.adoptionCalls, []);
  await fixture.controller.loadSessions();
  assert.deepEqual(fixture.adoptionCalls, []);
  newHistory = true;
  await fixture.controller.loadSessions();
  assert.deepEqual(fixture.adoptionCalls, [['new-claude', 'claude-new']]);
  assert.ok(visibleIDs(fixture).includes('claude-new'));
});

test('failed Claude preflight cannot launch or adopt a terminal', async () => {
  let launches = 0;
  const fixture = makeFilterFixture({
    saved: new Map([['session-provider-filter', '["codex"]']]),
    read: async (ids) => ({ sessions: [], succeededProviders: ids.filter((id) => id !== 'claude'), warnings: ids.includes('claude') ? ['unreadable'] : [] }),
    StartNew: async () => { launches++; return 'new-claude'; },
  });
  await fixture.controller.initialize();
  await fixture.controller.startNew('work', 'claude');
  assert.equal(launches, 0);
  assert.equal(fixture.state.terminals.size, 0);
  assert.match(fixture.statuses.at(-1).message, /不能安全建立新会话身份/);
});

test('older filter requests and errors cannot overwrite a newer selection', async () => {
  const fixture = makeFilterFixture();
  await fixture.controller.initialize();
  let rejectOld;
  fixture.backend.ListSessionsForProviders = () => new Promise((_, reject) => { rejectOld = reject; });
  const old = fixture.controller.toggleProvider('codex');
  fixture.backend.ListSessionsForProviders = async (ids) => ({ sessions: [filterCodex], succeededProviders: ids, warnings: [] });
  await fixture.controller.toggleProvider('codex');
  await fixture.controller.toggleProvider('claude');
  const statusCount = fixture.statuses.length;
  rejectOld(new Error('stale failure'));
  await old;
  assert.deepEqual([...fixture.state.selectedProviders], ['codex']);
  assert.deepEqual(visibleIDs(fixture), [filterCodex.id]);
  assert.equal(fixture.statuses.length, statusCount);
});

test('archive follows selection and rejects stale counts from older requests', async () => {
  const fixture = makeFilterFixture({ readHidden: async (ids) => ({ sessions: [filterClaude, filterCodex].filter((row) => ids.includes(row.provider)), succeededProviders: ids, warnings: [] }) });
  await fixture.controller.initialize();
  await fixture.hiddenButton.click();
  await fixture.controller.refreshHidden(true);
  assert.equal(fixture.hiddenCount.textContent, ' · 2');
  let resolveOld;
  fixture.backend.ListHiddenSessionsForProviders = () => new Promise((resolve) => { resolveOld = resolve; });
  const old = fixture.controller.refreshHidden(true);
  fixture.backend.ListHiddenSessionsForProviders = async (ids) => ({ sessions: [filterClaude], succeededProviders: ids, warnings: [] });
  await fixture.controller.toggleProvider('codex');
  await fixture.controller.refreshHidden(true);
  resolveOld({ sessions: [filterClaude, filterCodex], succeededProviders: ['claude', 'codex'], warnings: [] });
  await old;
  assert.equal(fixture.hiddenCount.textContent, ' · 1');
  assert.equal(fixture.hiddenPanel.children.length, 1);
});

test('late successful history response cannot replace a newer filter result', async () => {
  const fixture = makeFilterFixture();
  await fixture.controller.initialize();
  let resolveOld;
  fixture.backend.ListSessionsForProviders = () => new Promise((resolve) => { resolveOld = resolve; });
  const old = fixture.controller.loadSessions();
  fixture.backend.ListSessionsForProviders = async (ids) => ({ sessions: [filterClaude], succeededProviders: ids, warnings: [] });
  await fixture.controller.toggleProvider('codex');
  resolveOld({ sessions: [{ ...filterClaude, name: 'Stale name' }, filterCodex], succeededProviders: ['claude', 'codex'], warnings: [] });
  await old;
  assert.deepEqual(visibleIDs(fixture), [filterClaude.id]);
  assert.equal(fixture.state.sessionNames.get(filterClaude.id), filterClaude.name);
});

test('stop during restore identity lookup prevents history reads and terminal launches', async () => {
  let resolveRefs;
  let enteredLookup;
  const entered = new Promise((resolve) => { enteredLookup = resolve; });
  const fixture = makeFilterFixture({ GetOpenSessionRefs: () => new Promise((resolve) => { resolveRefs = resolve; enteredLookup(); }) });
  const initializing = fixture.controller.initialize();
  await entered;
  fixture.controller.stop();
  resolveRefs([{ id: filterCodex.id, provider: 'codex' }]);
  await initializing;
  assert.deepEqual(fixture.queries, []);
  assert.equal(fixture.state.terminals.size, 0);
});

test('automatic refresh waits for provider preferences before reading any history', async () => {
  let resolveProviders;
  const fixture = makeFilterFixture({
    saved: new Map([['session-provider-filter', '["claude"]']]),
    ListAgentProviders: () => new Promise((resolve) => { resolveProviders = resolve; }),
  });
  fixture.controller.start();
  const initializing = fixture.controller.initialize();
  await fixture.controller.autoRefreshSessions();
  const earlyQueries = fixture.queries.slice();
  resolveProviders(filterAgents);
  await initializing;
  await fixture.controller.autoRefreshSessions();
  assert.deepEqual(earlyQueries, []);
  assert.ok(fixture.queries.every((ids) => JSON.stringify(ids) === '["claude"]'));
});

test('automatic refresh cannot cancel a user initiated Claude history preflight', async () => {
  let launches = 0;
  const fixture = makeFilterFixture({
    saved: new Map([['session-provider-filter', '["codex"]']]),
    StartNew: async () => { launches++; return 'new-claude'; },
  });
  await fixture.controller.initialize();
  fixture.controller.start();
  const resolveReads = [];
  let reads = 0;
  fixture.backend.ListSessionsForProviders = () => { reads++; return new Promise((resolve) => { resolveReads.push(resolve); }); };
  const launch = fixture.controller.startNew('work', 'claude');
  const refresh = fixture.controller.autoRefreshSessions();
  for (const resolve of resolveReads) resolve({ sessions: [filterClaude], succeededProviders: ['claude'], warnings: [] });
  await Promise.all([launch, refresh]);
  assert.equal(reads, 1);
  assert.equal(launches, 1);
  assert.deepEqual(fixture.adoptionCalls, []);
});

function renderProjectGroups(
  projects,
  list,
  onStartNew = () => {},
  onToggleGroup = () => {},
  favoriteProjects = [],
) {
  const listRoot = new FakeNode();
  renderSessionList({
    listRoot,
    projects,
    list,
    state: createAppState(),
    favoriteProjects,
    agentController: { classifyAgent: () => 'idle' },
    el: (tag, className, text) => {
      const node = new FakeNode();
      node.className = className;
      node.textContent = text || '';
      return node;
    },
    onStartNew,
    onToggleGroup,
    onOpen() {},
    onClose() {},
    onContextMenu() {},
  });
  return listRoot;
}

test('listSig tracks time as well as id, directory, and name', () => {
  const first = [session('a', 'work', 'A', 'one')];
  assert.notEqual(listSig(first), listSig([session('a', 'work', 'A', 'two')]));
  assert.notEqual(listSig(first), listSig([session('b', 'work', 'A')]));
  assert.notEqual(listSig(first), listSig([session('a', 'other', 'A')]));
  assert.notEqual(listSig(first), listSig([session('a', 'work', 'B')]));
});

test('Claude pending sessions never pair with Codex history in the same directory', () => {
  const pending = [{ token: 'new-1', dir: 'C:/work' }];
  const newToReal = new Map();
  const remaining = pairPendingSessions({ pending, lastLoaded: [], list: [
    { id: 'codex:existing', provider: 'codex', dir: 'C:/work' },
  ], newToReal, realToNew: new Map() });
  assert.deepEqual(remaining, pending);
  assert.equal(newToReal.size, 0);
});

test('Codex temporary terminals remain visible without adopting new history', async () => {
  const calls = [];
  const f = makeFixture({ StartNewAgent: async (provider, dir) => {
    calls.push([provider, dir]); return 'new-codex-1';
  } });
  await f.controller.startNew('C:/work', 'codex');
  assert.deepEqual(calls, [['codex', 'C:/work']]);
  assert.equal(f.state.terminals.get('new-codex-1').provider, 'codex');
  assert.equal(f.state.pendingNew[0].provider, 'codex');
  const row = f.listRoot.children[0].children[1].children[0];
  assert.equal(row.dataset.id, 'new-codex-1');
  await f.controller.pairNewSessions([{ id: 'codex:real', provider: 'codex', dir: 'C:/work' }]);
  assert.equal(f.adoptionCalls.length, 0);
  assert.equal(f.state.pendingNew.length, 1);
  f.controller.handleTerminalExit('new-codex-1');
  assert.equal(f.state.pendingNew.length, 0);
});

test('project plus offers registered agents and routes Codex to its backend', async () => {
  const calls = [];
  const f = makeFixture({
    projectResults: [['C:/work']],
    ListAgentProviders: async () => [
      { id: 'claude', name: 'Claude', hasUsage: true, hasStatus: true },
      { id: 'codex', name: 'Codex', hasUsage: false, hasStatus: false },
    ],
    StartNewAgent: async (provider, dir) => { calls.push([provider, dir]); return 'new-codex-menu'; },
  });
  await f.controller.initialize();
  const plus = f.listRoot.children[0].children[0].children.find((node) => node.className === 'plus');
  plus.click();
  const menu = f.documentRef.body.children.find((node) => node.id === 'ctx-menu');
  assert.deepEqual(menu.children.map((node) => node.textContent), ['新建 Claude 会话', '新建 Codex 会话']);
  await menu.children[1].click();
  assert.deepEqual(calls, [['codex', 'C:/work']]);
});

test('session rows keep their custom context menu and prevent the native menu', () => {
  const listRoot = new FakeNode();
  let opened = null;
  renderSessionList({
    listRoot,
    list: [session('session-1')],
    state: createAppState(),
    agentController: { classifyAgent: () => 'idle' },
    el: (tag, className, text) => {
      const node = new FakeNode();
      node.className = className;
      node.textContent = text || '';
      return node;
    },
    onStartNew() {},
    onToggleGroup() {},
    onOpen() {},
    onClose() {},
    onContextMenu: (x, y, target) => { opened = { x, y, target }; },
  });

  const row = listRoot.children[0].children[1].children[0];
  const event = {
    clientX: 12,
    clientY: 34,
    preventDefault() { this.prevented = true; },
  };
  row.listeners.get('contextmenu')(event);

  assert.equal(event.prevented, true);
  assert.deepEqual(opened, {
    x: 12,
    y: 34,
    target: { type: 'session', id: 'session-1', dir: 'work', name: 'session-1', provider: 'claude' },
  });
});

test('directory group heads open the custom context menu for their folder', () => {
  const listRoot = new FakeNode();
  let opened = null;
  renderSessionList({
    listRoot,
    list: [session('session-1', 'C:\\work\\project')],
    state: createAppState(),
    agentController: { classifyAgent: () => 'idle' },
    el: (tag, className, text) => {
      const node = new FakeNode();
      node.className = className;
      node.textContent = text || '';
      return node;
    },
    onStartNew() {},
    onToggleGroup() {},
    onOpen() {},
    onClose() {},
    onContextMenu: (x, y, target) => { opened = { x, y, target }; },
  });

  const head = listRoot.children[0].children[0];
  const event = {
    clientX: 12,
    clientY: 34,
    preventDefault() { this.prevented = true; },
  };
  head.listeners.get('contextmenu')(event);

  assert.equal(event.prevented, true);
  assert.deepEqual(opened, {
    x: 12,
    y: 34,
    target: {
      type: 'directory', dir: 'C:\\work\\project', favorite: false,
    },
  });
});

test('group heads show a stateful folder icon and an icon-only new-session button', () => {
  const listRoot = new FakeNode();
  renderSessionList({
    listRoot,
    list: [session('session-1', 'work')],
    state: createAppState(),
    agentController: { classifyAgent: () => 'idle' },
    usageByProject: new Map([['work', {
      project_found: true,
      project_total: { input_tokens: 1000, output_tokens: 20 },
    }]]),
    el: (tag, className, text) => {
      const node = new FakeNode();
      node.className = className;
      node.textContent = text || '';
      return node;
    },
    onStartNew() {},
    onToggleGroup() {},
    onOpen() {},
    onClose() {},
    onContextMenu() {},
  });
  const head = listRoot.children[0].children[0];
  assert.equal(head.children[0].className, 'folder-icon');
  assert.match(head.children[0].innerHTML, /folder-open/);
  assert.match(head.children[0].innerHTML, /folder-closed/);
  assert.match(head.children[0].innerHTML, /viewBox="0 0 24 24"/);
  assert.match(head.children[0].innerHTML, /stroke-width="1\.8"/);
  assert.match(head.children[0].innerHTML, /m6 14 1\.5-2\.9A2 2/);
  assert.match(head.children[0].innerHTML, /M20 20a2 2 0 0 0 2-2/);
  assert.equal(head.children.some((child) => child.className === 'chevron'), false);
  assert.equal(head.children[2].className, 'group-usage');
  assert.equal(head.children[2].textContent, '1.02K');
  assert.equal(head.children[3].className, 'plus');
  assert.match(head.children[3].innerHTML, /<svg/);
  assert.equal(head.children[3].type, 'button');
  assert.match(head.children[3].innerHTML, /M6 22a2 2/);
  assert.match(head.children[3].innerHTML, /M12 18v-6/);
  assert.match(head.children[2].title, /项目累计 1.02K/);
});

test('favorited project groups render first and highlight their folder icon', () => {
  const favorite = 'C:\\work\\favorite';
  const regular = 'C:\\work\\regular';
  const listRoot = renderProjectGroups(
    [regular, favorite],
    [session('regular', regular), session('favorite', favorite)],
    () => {},
    () => {},
    [favorite],
  );
  const groups = listRoot.children.filter((node) => node.className === 'group');

  assert.equal(groups.length, 2);
  assert.equal(groups[0].children[0].dataset.dir, favorite.toLowerCase().replaceAll('\\', '/'));
  assert.equal(groups[0].children[0].children[0].classList.contains('favorite'), true);
  assert.equal(groups[1].children[0].children[0].classList.contains('favorite'), false);
});

test('collapsed group renders the closed folder variant', () => {
  const state = createAppState();
  state.collapsedDirs.add('work');
  const listRoot = new FakeNode();
  renderSessionList({
    listRoot,
    list: [session('session-1', 'work')],
    state,
    agentController: { classifyAgent: () => 'idle' },
    el: (tag, className, text) => {
      const node = new FakeNode();
      node.className = className;
      node.textContent = text || '';
      return node;
    },
    onStartNew() {},
    onToggleGroup() {},
    onOpen() {},
    onClose() {},
    onContextMenu() {},
  });

  const group = listRoot.children[0];
  assert.equal(group.classList.contains('collapsed'), true);
  assert.match(group.children[0].children[0].innerHTML, /folder-closed/);
});

test('rendering a session list invokes the single project prefetch entry point', () => {
  const fixture = makeFixture();
  const sessions = [session('one', 'work'), session('two', 'other')];
  fixture.controller.renderSessions(sessions);
  assert.deepEqual(fixture.projectCalls, [sessions]);
});

test('a saved project with no sessions renders one empty group', () => {
  const dir = 'C:\\work\\empty';
  const listRoot = renderProjectGroups([dir], []);

  assert.equal(listRoot.children.filter((node) => node.className === 'group').length, 1);
  assert.equal(listRoot.children[0].children[1].children.length, 0);
});

test('directory context menu calls the backend to open the project folder', async () => {
  const dir = 'C:\\work\\open-me';
  const fixture = makeFixture({ projectResults: [[dir]], listResults: [[]] });

  await fixture.controller.initialize();

  const head = fixture.listRoot.children[0].children[0];
  const event = {
    clientX: 12,
    clientY: 34,
    preventDefault() { this.prevented = true; },
  };
  head.listeners.get('contextmenu')(event);
  const menu = fixture.documentRef.body.children.find((node) => node.id === 'ctx-menu');

  assert.equal(event.prevented, true);
  assert.equal(menu.children.length, 2);
  assert.equal(menu.children[1].textContent, '打开文件夹');
  await menu.children[1].click();
  assert.deepEqual(fixture.folderCalls, [dir]);
});

test('a session-only directory group can be favorited', async () => {
  const dir = 'C:\\work\\session-only';
  const fixture = makeFixture({
    projectResults: [[]],
    listResults: [[session('one', dir)]],
    favoriteResults: [[]],
  });

  await fixture.controller.initialize();

  const head = fixture.listRoot.children[0].children[0];
  head.listeners.get('contextmenu')({
    clientX: 12,
    clientY: 34,
    preventDefault() {},
  });
  const menu = fixture.documentRef.body.children.find((node) => node.id === 'ctx-menu');

  assert.equal(menu.children[0].textContent, '添加收藏');
  await menu.children[0].click();

  assert.deepEqual(fixture.favoriteCalls, [[dir, true]]);
  assert.deepEqual(fixture.state.projectFavorites, [dir]);
  const groups = fixture.listRoot.children.filter((node) => node.className === 'group');
  assert.equal(groups[0].children[0].children[0].classList.contains('favorite'), true);
});

test('failed directory favorite writes leave the group unchanged', async () => {
  const dir = 'C:\\work\\favorite-failure';
  const fixture = makeFixture({
    projectResults: [[]],
    listResults: [[session('one', dir)]],
    favoriteResults: [[]],
    SetProjectFavorite: async () => { throw new Error('disk full'); },
  });

  await fixture.controller.initialize();
  const head = fixture.listRoot.children[0].children[0];
  head.listeners.get('contextmenu')({ clientX: 12, clientY: 34, preventDefault() {} });
  const menu = fixture.documentRef.body.children.find((node) => node.id === 'ctx-menu');

  await menu.children[0].click();

  assert.deepEqual(fixture.state.projectFavorites, []);
  assert.equal(fixture.listRoot.children[0].children[0].children[0].classList.contains('favorite'), false);
  assert.match(fixture.statuses.at(-1).message, /添加收藏失败/);
});

test('adding a project favorite pins its group and updates the folder icon', async () => {
  const favorite = 'C:\\work\\favorite';
  const regular = 'C:\\work\\regular';
  const fixture = makeFixture({
    projectResults: [[regular, favorite]],
    listResults: [[]],
    favoriteResults: [[]],
  });

  await fixture.controller.initialize();

  const regularHead = fixture.listRoot.children[0].children[0];
  regularHead.listeners.get('contextmenu')({
    clientX: 12,
    clientY: 34,
    preventDefault() { this.prevented = true; },
  });
  const menu = fixture.documentRef.body.children.find((node) => node.id === 'ctx-menu');

  assert.equal(menu.children[0].textContent, '添加收藏');
  await menu.children[0].click();

  assert.deepEqual(fixture.favoriteCalls, [[regular, true]]);
  assert.deepEqual(fixture.state.projectFavorites, [regular]);
  const groups = fixture.listRoot.children.filter((node) => node.className === 'group');
  const favoriteGroup = groups[0];
  assert.equal(favoriteGroup.children[0].dataset.dir, regular.toLowerCase().replaceAll('\\', '/'));
  assert.equal(favoriteGroup.children[0].children[0].classList.contains('favorite'), true);
});

test('a favorited project offers remove favorite and restores normal ordering', async () => {
  const favorite = 'C:\\work\\favorite';
  const regular = 'C:\\work\\regular';
  const fixture = makeFixture({
    projectResults: [[regular, favorite]],
    listResults: [[]],
    favoriteResults: [[favorite]],
  });

  await fixture.controller.initialize();

  const favoriteHead = fixture.listRoot.children[0].children[0];
  favoriteHead.listeners.get('contextmenu')({
    clientX: 12,
    clientY: 34,
    preventDefault() {},
  });
  const menu = fixture.documentRef.body.children.find((node) => node.id === 'ctx-menu');

  assert.equal(menu.children[0].textContent, '取消收藏');
  await menu.children[0].click();

  assert.deepEqual(fixture.favoriteCalls, [[favorite, false]]);
  assert.deepEqual(fixture.state.projectFavorites, []);
  const groups = fixture.listRoot.children.filter((node) => node.className === 'group');
  assert.equal(groups[0].children[0].children[1].textContent, 'regular');
  assert.equal(groups[1].children[0].children[1].textContent, 'favorite');
});

test('a project and a session with the same directory render one group', () => {
  const projectDir = 'C:\\Work\\Alpha';
  const sessionDir = 'c:/work/alpha/';
  const listRoot = renderProjectGroups([projectDir], [session('one', sessionDir)]);

  assert.equal(listRoot.children.filter((node) => node.className === 'group').length, 1);
  assert.equal(listRoot.children[0].children[1].children.length, 1);
});

test('case, slash, and non-root trailing-slash differences do not duplicate a group', () => {
  const listRoot = renderProjectGroups([
    'C:\\Work\\Alpha',
    'c:/work/alpha/',
  ], []);

  assert.equal(listRoot.children.filter((node) => node.className === 'group').length, 1);
});

test('an empty project group plus starts a session with the saved directory', () => {
  const dir = 'C:\\work\\empty';
  const started = [];
  const listRoot = renderProjectGroups([dir], [], (projectDir) => started.push(projectDir));

  const plus = listRoot.children[0].children[0].children.find((child) => child.className === 'plus');
  plus.click();
  assert.deepEqual(started, [dir]);
});

test('group plus does not toggle its group header', () => {
  let toggled = 0;
  const listRoot = renderProjectGroups(
    ['C:\\work\\empty'],
    [],
    () => {},
    () => { toggled += 1; },
  );

  const plus = listRoot.children[0].children[0].children.find((child) => child.className === 'plus');
  plus.click();

  assert.equal(toggled, 0);
});

test('a normalized project/session group plus passes the saved project directory', () => {
  const projectDir = 'C:\\Work\\Alpha';
  const sessionDir = 'c:/work/alpha/';
  const started = [];
  const listRoot = renderProjectGroups(
    [projectDir],
    [session('one', sessionDir)],
    (dir) => started.push(dir),
  );

  listRoot.children[0].children[0].children.find((child) => child.className === 'plus').click();
  assert.deepEqual(started, [projectDir]);
});

test('directories with the same leaf name remain separate groups', () => {
  const listRoot = renderProjectGroups([
    'C:\\one\\same',
    'C:\\two\\same',
  ], []);

  assert.equal(listRoot.children.filter((node) => node.className === 'group').length, 2);
  assert.deepEqual(listRoot.children.map(group => group.children[0].children[1].textContent), [
    'same · one', 'same · two',
  ]);
});

test('same-name saved project and worktree history have distinct labels and launch directories', () => {
  const saved = 'D:\\projects\\sample';
  const worktree = 'C:\\workspace\\worktrees\\task-one\\sample';
  const started = [];
  const listRoot = renderProjectGroups([saved], [session('worktree-session', worktree)], dir => started.push(dir));
  const heads = listRoot.children.map(group => group.children[0]);
  assert.deepEqual(heads.map(head => head.children[1].textContent), ['sample · task-one', 'sample · projects']);
  assert.deepEqual(heads.map(head => head.children[1].title), [worktree, saved]);
  assert.deepEqual(listRoot.children.map(group => group.children[1].children.length), [1, 0]);
  for (const head of heads) {
    const plus = head.children.find(child => child.className === 'plus');
    assert.ok(plus.attributes.get('aria-label').includes(head.children[1].textContent));
    plus.click();
  }
  assert.deepEqual(started, [worktree, saved]);
});

test('same-name labels extend the parent suffix until distinct, including drive and UNC server', () => {
  for (const [dirs, labels] of [
    [['C:\\one\\shared\\app', 'D:\\two\\shared\\app'], ['app · one/shared', 'app · two/shared']],
    [['C:\\work\\app', 'D:\\work\\app'], ['app · C:/work', 'app · D:/work']],
    [['\\\\server-one\\share\\app', '\\\\server-two\\share\\app'], ['app · server-one/share', 'app · server-two/share']],
  ]) {
    const listRoot = renderProjectGroups(dirs, []);
    assert.deepEqual(listRoot.children.map(group => group.children[0].children[1].textContent), labels);
  }
});

test('case differences in leaf labels still disambiguate different Windows directories', () => {
  const listRoot = renderProjectGroups(['C:\\one\\App', 'D:\\two\\app'], []);
  assert.deepEqual(listRoot.children.map(group => group.children[0].children[1].textContent), ['App · one', 'app · two']);
});

test('normalized duplicate directories keep a single short label from the saved project', () => {
  const saved = 'C:\\Work\\Alpha';
  const listRoot = renderProjectGroups([saved, 'c:/work/alpha/'], [session('one', 'c:/work/alpha/')]);
  assert.equal(listRoot.children.length, 1);
  assert.equal(listRoot.children[0].children[0].children[1].textContent, 'Alpha');
  assert.equal(listRoot.children[0].children[0].children[1].title, saved);
});

test('unique projects and drive roots keep their short labels', () => {
  const listRoot = renderProjectGroups(['C:\\', 'D:\\', 'C:\\work\\unique\\'], []);
  assert.deepEqual(listRoot.children.map(group => group.children[0].children[1].textContent), ['C:\\', 'D:\\', 'unique']);
});

test('cancelled project chooser does not call AddProject', async () => {
  const fixture = makeFixture({ chosenDir: '' });

  await fixture.addProjectButton.click();

  assert.equal(fixture.chooserCalls.length, 1);
  assert.deepEqual(fixture.projectAddCalls, []);
});

test('project load failure reports status but still loads sessions and open sessions', async () => {
  const fixture = makeFixture({ ListProjects: async () => { throw new Error('projects offline'); } });

  await fixture.controller.initialize();

  assert.equal(fixture.listCalls.length, 1);
  assert.equal(fixture.openCalls.length, 1);
  assert.match(fixture.statuses[0].message, /项目/);
});

test('project add button binds once across repeated project refreshes', async () => {
  const fixture = makeFixture({ chosenDir: 'C:\\work\\alpha' });

  await fixture.controller.loadProjects();
  await fixture.controller.loadProjects();
  await fixture.addProjectButton.click();

  assert.equal(fixture.addProjectButton.listenerCounts.get('click'), 1);
  assert.equal(fixture.chooserCalls.length, 1);
});

test('adding a chosen project refreshes session-list with an empty directory group', async () => {
  const dir = 'C:\\work\\chosen';
  const fixture = makeFixture({ chosenDir: dir, projectResults: [[], [dir]], listResults: [[]] });

  await fixture.controller.initialize();
  await fixture.addProjectButton.click();

  assert.deepEqual(fixture.projectAddCalls, [dir]);
  assert.deepEqual(fixture.state.projects, [dir]);
  const groups = fixture.listRoot.children.filter((node) => node.className === 'group');
  assert.equal(groups.length, 1);
  assert.equal(groups[0].children[0].className, 'group-head');
  assert.equal(groups[0].children[1].className, 'group-body');
  assert.equal(groups[0].children[1].children.length, 0);
  assert.equal(groups[0].children[0].children.find((child) => child.className === 'plus').className, 'plus');
});

test('initialize renders a saved project as an empty session-list group', async () => {
  const dir = 'C:\\work\\startup';
  const fixture = makeFixture({ projectResults: [[dir]], listResults: [[]] });

  await fixture.controller.initialize();

  const groups = fixture.listRoot.children.filter((node) => node.className === 'group');
  assert.equal(groups.length, 1);
  assert.equal(groups[0].children[0].className, 'group-head');
  assert.equal(groups[0].children[1].className, 'group-body');
  assert.equal(groups[0].children[1].children.length, 0);
});

test('saved project group plus start failure leaves pending sessions empty', async () => {
  const dir = 'C:\\missing\\later';
  const fixture = makeFixture({
    projectResults: [[dir]],
    listResults: [[]],
    StartNew: async () => { throw new Error('start failed'); },
  });

  await fixture.controller.initialize();
  const groups = fixture.listRoot.children.filter((node) => node.className === 'group');
  await groups[0].children[0].children.find((child) => child.className === 'plus').click();

  assert.deepEqual(fixture.state.pendingNew, []);
  assert.match(fixture.statuses.at(-1).message, /新建失败/);
});

test('session restoration uses the session directory instead of the project list', async () => {
  const projectDir = 'C:\\configured\\project';
  const sessionDir = 'C:\\actual\\session-dir';
  const started = [];
  const fixture = makeFixture({
    projectResults: [[projectDir]],
    listResults: [[session('real', sessionDir)]],
    GetOpenSessions: async () => ['real'],
    StartSession: async (id, dir) => { started.push([id, dir]); },
  });

  await fixture.controller.initialize();

  assert.deepEqual(started, [['real', sessionDir]]);
});

test('pairPendingSessions maps same-directory pending entries FIFO', () => {
  const realToNew = new Map([['existing', 'new-existing']]);
  const newToReal = new Map();
  const paired = [];
  const remaining = pairPendingSessions({
    pending: [
      { token: 'new-1', dir: 'work' },
      { token: 'new-2', dir: 'work' },
      { token: 'new-3', dir: 'other' },
    ],
    lastLoaded: [session('old', 'work')],
    list: [session('real-1', 'work', 'One'), session('real-2', 'work', 'Two'), session('existing', 'work')],
    realToNew,
    newToReal,
    onPair: (pending, real) => paired.push([pending.token, real]),
  });
  assert.deepEqual(paired, [['new-1', 'real-1'], ['new-2', 'real-2']]);
  assert.deepEqual(remaining, [{ token: 'new-3', dir: 'other' }]);
  assert.equal(newToReal.get('new-1'), 'real-1');
});

test('openFromList activates existing, rebuilds exited, and cleans failed starts', async () => {
  const fixture = makeFixture({ StartSession: async (id) => {
    if (id === 'bad') throw new Error('failed');
  } });
  fixture.state.unreadSessions.add('running');
  fixture.terminalController.openTab('running', 'Running');
  await fixture.controller.openFromList(session('running'));
  assert.deepEqual(fixture.terminalController.activations, ['running']);
  assert.equal(fixture.state.terminals.get('running').dir, 'work');
  assert.equal(fixture.state.unreadSessions.has('running'), false);

  const exited = fixture.terminalController.openTab('bad', 'Bad');
  exited.exited = true;
  await fixture.controller.openFromList(session('bad'));
  assert.deepEqual(fixture.terminalController.disposed, ['bad', 'bad']);
  assert.equal(fixture.state.terminals.has('bad'), false);

  await fixture.controller.openFromList(session('good'));
  assert.equal(fixture.state.terminals.has('good'), true);
  assert.equal(fixture.state.terminals.get('good').dir, 'work');
});

test('pane-aware opening assigns sessions without using the legacy single-terminal activation', async () => {
  const calls = [];
  const paneController = {
    showSession: (token, options) => { calls.push(['show', token, options]); return true; },
    getVisiblePaneIds: () => ['pane-0', 'pane-1'],
    focusFirstAssigned: () => calls.push(['focus-first']),
    setSessionOptions: () => {},
  };
  const fixture = makeFixture({
    paneController,
    listResults: [[session('one'), session('two')]],
    GetOpenSessions: async () => ['one', 'two'],
  });

  await fixture.controller.initialize();

  assert.deepEqual(fixture.terminalController.activations, []);
  assert.deepEqual(calls, [
    ['show', 'one', { paneId: 'pane-0', focus: false }],
    ['show', 'two', { paneId: 'pane-1', focus: false }],
    ['focus-first'],
  ]);
});

test('pane-aware restoration uses the layout-specific four-pane fill order', async () => {
  const calls = [];
  const paneController = {
    showSession: (token, options) => { calls.push(['show', token, options]); return true; },
    getVisiblePaneIds: () => ['pane-0', 'pane-1', 'pane-2', 'pane-3'],
    getPaneFillOrder: () => ['pane-0', 'pane-2', 'pane-1', 'pane-3'],
    focusFirstAssigned: () => calls.push(['focus-first']),
    setSessionOptions: () => {},
  };
  const fixture = makeFixture({
    paneController,
    listResults: [[session('one'), session('two'), session('three'), session('four')]],
    GetOpenSessions: async () => ['one', 'two', 'three', 'four'],
  });

  await fixture.controller.initialize();

  assert.deepEqual(calls, [
    ['show', 'one', { paneId: 'pane-0', focus: false }],
    ['show', 'two', { paneId: 'pane-2', focus: false }],
    ['show', 'three', { paneId: 'pane-1', focus: false }],
    ['show', 'four', { paneId: 'pane-3', focus: false }],
    ['focus-first'],
  ]);
});

test('pane-aware restoration does not replace earlier sessions when more are open than panes', async () => {
  const calls = [];
  const starts = [];
  const paneController = {
    showSession: (token, options) => { calls.push(['show', token, options]); return true; },
    getVisiblePaneIds: () => ['pane-0', 'pane-1'],
    focusFirstAssigned: () => calls.push(['focus-first']),
    setSessionOptions: () => {},
  };
  const fixture = makeFixture({
    paneController,
    listResults: [[session('one'), session('two'), session('three')]],
    StartSession: async (...args) => { starts.push(args); },
    GetOpenSessions: async () => ['one', 'two', 'three'],
  });

  await fixture.controller.initialize();

  assert.deepEqual(calls, [
    ['show', 'one', { paneId: 'pane-0', focus: false }],
    ['show', 'two', { paneId: 'pane-1', focus: false }],
    ['focus-first'],
  ]);
  assert.deepEqual(starts, [['one', 'work'], ['two', 'work'], ['three', 'work']]);
});

test('async session opening keeps the pane chosen before backend startup', async () => {
  const calls = [];
  let targetPane = 'pane-0';
  let resolveStart;
  const paneController = {
    getTargetPaneId: () => targetPane,
    showSession: (token, options) => { calls.push([token, options]); return true; },
    setSessionOptions: () => {},
  };
  const fixture = makeFixture({
    paneController,
    StartSession: () => new Promise((resolve) => { resolveStart = resolve; }),
  });

  const pending = fixture.controller.openFromList(session('async'));
  targetPane = 'pane-1';
  resolveStart();
  await pending;

  assert.deepEqual(calls, [['async', { paneId: 'pane-0' }]]);
});

test('async new session opening keeps the pane chosen before backend startup', async () => {
  const calls = [];
  let targetPane = 'pane-0';
  let resolveStart;
  const paneController = {
    getTargetPaneId: () => targetPane,
    showSession: (token, options) => { calls.push([token, options]); return true; },
    setSessionOptions: () => {},
  };
  const fixture = makeFixture({
    paneController,
    StartNew: () => new Promise((resolve) => { resolveStart = () => resolve('async-new'); }),
  });

  const pending = fixture.controller.startNew('work');
  targetPane = 'pane-1';
  resolveStart();
  await pending;

  assert.deepEqual(calls, [['async-new', { paneId: 'pane-0' }]]);
});

test('pane-aware new sessions are shown in the pane layer after backend startup', async () => {
  const calls = [];
  const paneController = {
    showSession: (token, options) => { calls.push([token, options]); return true; },
    setSessionOptions: () => {},
  };
  const fixture = makeFixture({ paneController, StartNew: async () => 'new-pane' });

  await fixture.controller.startNew('work');

  assert.deepEqual(calls, [['new-pane', {}]]);
  assert.deepEqual(fixture.terminalController.activations, []);
});

test('startNew only records pending state after successful backend start', async () => {
  const failed = makeFixture({ StartNew: async () => { throw new Error('failed'); } });
  await failed.controller.startNew('work');
  assert.deepEqual(failed.state.pendingNew, []);

  const created = makeFixture({ StartNew: async () => 'new-success' });
  await created.controller.startNew('work');
  assert.deepEqual(created.state.pendingNew, [{ token: 'new-success', dir: 'work' }]);
  assert.equal(created.state.terminals.get('new-success').dir, 'work');
  assert.deepEqual(created.terminalController.activations, ['new-success']);
});

test('controller pairing updates the temporary label for a new real session', async () => {
  const fixture = makeFixture();
  fixture.controller.renderSessions([session('old', 'work', 'Old')]);
  fixture.state.activeToken = 'new-1';
  fixture.state.pendingNew.push({ token: 'new-1', dir: 'work' });
  fixture.terminalController.openTab('new-1', '新会话 1');

  await fixture.controller.pairNewSessions([
    session('old', 'work', 'Old'),
    session('real', 'work', 'Real name'),
  ]);

  assert.equal(fixture.state.realToNew.get('real'), 'new-1');
  assert.equal(fixture.state.terminals.get('new-1').labelText, 'Real name');
  assert.deepEqual(fixture.state.pendingNew, []);
  assert.deepEqual(fixture.adoptionCalls, [['new-1', 'real']]);
});

test('controller adopts multiple paired sessions in FIFO order', async () => {
  const fixture = makeFixture();
  fixture.controller.renderSessions([session('old', 'work', 'Old')]);
  fixture.state.pendingNew.push(
    { token: 'new-1', dir: 'work' },
    { token: 'new-2', dir: 'work' },
  );
  fixture.terminalController.openTab('new-1', '新会话 1');
  fixture.terminalController.openTab('new-2', '新会话 2');

  await fixture.controller.pairNewSessions([
    session('old', 'work', 'Old'),
    session('real-1', 'work', 'One'),
    session('real-2', 'work', 'Two'),
  ]);

  assert.deepEqual(fixture.adoptionCalls, [
    ['new-1', 'real-1'],
    ['new-2', 'real-2'],
  ]);
});

test('controller retries a failed adoption on the next refresh', async () => {
  let attempts = 0;
  const fixture = makeFixture({
    AdoptSession: async (token, realId) => {
      attempts += 1;
      fixture.adoptionCalls.push([token, realId]);
      if (attempts === 1) throw new Error('disk full');
    },
  });
  fixture.controller.renderSessions([session('old', 'work', 'Old')]);
  fixture.state.pendingNew.push({ token: 'new-retry', dir: 'work' });
  fixture.terminalController.openTab('new-retry', '新会话');
  const list = [session('old', 'work', 'Old'), session('real-retry', 'work', 'Retry')];

  await fixture.controller.pairNewSessions(list);
  assert.deepEqual(fixture.adoptionCalls, [['new-retry', 'real-retry']]);

  await fixture.controller.pairNewSessions(list);
  assert.deepEqual(fixture.adoptionCalls, [
    ['new-retry', 'real-retry'],
    ['new-retry', 'real-retry'],
  ]);
});

test('terminal exit drops a failed adoption instead of retrying a dead token', async () => {
  let attempts = 0;
  const fixture = makeFixture({
    AdoptSession: async () => {
      attempts += 1;
      throw new Error('token exited');
    },
  });
  fixture.controller.renderSessions([session('old', 'work', 'Old')]);
  fixture.state.pendingNew.push({ token: 'new-dead', dir: 'work' });
  fixture.terminalController.openTab('new-dead', '新会话');
  const list = [session('old', 'work', 'Old'), session('real-dead', 'work', 'Dead')];

  await fixture.controller.pairNewSessions(list);
  fixture.controller.handleTerminalExit('new-dead');
  await fixture.controller.pairNewSessions(list);

  assert.equal(attempts, 1);
});

test('stopping during an in-flight adoption invalidates its result', async () => {
  let resolveAdoption;
  const fixture = makeFixture({
    AdoptSession: () => new Promise((resolve) => { resolveAdoption = resolve; }),
  });
  fixture.controller.renderSessions([session('old', 'work', 'Old')]);
  fixture.state.pendingNew.push({ token: 'new-stop', dir: 'work' });
  fixture.terminalController.openTab('new-stop', '新会话');
  const pairing = fixture.controller.pairNewSessions([
    session('old', 'work', 'Old'), session('real-stop', 'work', 'Stop'),
  ]);
  await new Promise((resolve) => setImmediate(resolve));
  fixture.controller.stop();
  resolveAdoption();
  await pairing;

  fixture.controller.start();
  await fixture.controller.autoRefreshSessions();
  assert.deepEqual(fixture.adoptionCalls, []);
});

test('full refresh pairs pending sessions before replacing the loaded snapshot', async () => {
  const old = session('old', 'work', 'Old');
  const real = session('real', 'work', 'Real name');
  const fixture = makeFixture({ listResults: [[old, real]] });
  fixture.controller.renderSessions([old]);
  fixture.state.pendingNew.push({ token: 'new-1', dir: 'work' });
  fixture.terminalController.openTab('new-1', '新会话 1');

  await fixture.controller.loadSessions();

  assert.equal(fixture.state.realToNew.get('real'), 'new-1');
  assert.equal(fixture.state.newToReal.get('new-1'), 'real');
  assert.deepEqual(fixture.state.pendingNew, []);
  assert.equal(fixture.state.terminals.get('new-1').labelText, 'Real name');
});

test('rename updates the mapped temporary terminal label', async () => {
  const fixture = makeFixture();
  fixture.state.realToNew.set('real', 'new-real');
  fixture.state.newToReal.set('new-real', 'real');
  fixture.terminalController.openTab('new-real', 'Old');
  await fixture.controller.renameSession(session('real', 'work', 'Old'));
  assert.equal(fixture.state.terminals.get('new-real').labelText, 'Renamed');
});

test('native rename rejects blank names and preserves UI after native failure', async () => {
  let calls = 0;
  const blank = makeFixture({ prompt: () => '   ', RenameSession: async () => { calls++; } });
  await blank.controller.renameSession(session('real', 'work', 'Old'));
  assert.equal(calls, 0);
  assert.equal(blank.statuses.at(-1).message, '会话名称不能为空');
  const failed = makeFixture({ RenameSession: async () => { throw new Error('native denied'); } });
  failed.terminalController.openTab('real', 'Old');
  await failed.controller.renameSession(session('real', 'work', 'Old'));
  assert.equal(failed.state.terminals.get('real').labelText, 'Old');
  assert.equal(failed.state.sessionNames.has('real'), false);
  assert.equal(failed.listCalls.length, 0);
  assert.match(failed.statuses.at(-1).message, /native denied/);
});

test('Codex native rename uses the provider key and refreshes authoritative names', async () => {
  const key = 'codex:01900000-0000-7000-8000-000000000001';
  const title = '中文 "名字" C:\\work & $(literal)';
  const calls = [];
  const fixture = makeFixture({
    prompt: (message) => { assert.match(message, /原生/); return ' ' + title + ' '; },
    RenameSession: async (...args) => { calls.push(args); },
    listResults: [[{ ...session(key, 'work', title), provider: 'codex' }]],
  });
  fixture.terminalController.openTab(key, 'Old');
  await fixture.controller.renameSession({ ...session(key, 'work', 'Old'), provider: 'codex' });
  assert.deepEqual(calls, [[key, title]]);
  assert.equal(fixture.state.terminals.get(key).labelText, title);
  assert.equal(fixture.state.sessionNames.get(key), title);
  assert.equal(fixture.listCalls.length, 1);
});

test('successful native rename preserves its name and warns when history refresh fails', async () => {
  let calls = 0;
  const fixture = makeFixture({
    RenameSession: async () => { calls++; },
    listResults: [new Error('history unavailable')],
  });
  fixture.terminalController.openTab('real', 'Old');
  await fixture.controller.renameSession(session('real', 'work', 'Old'));
  assert.equal(calls, 1);
  assert.equal(fixture.state.terminals.get('real').labelText, 'Renamed');
  assert.equal(fixture.state.sessionNames.get('real'), 'Renamed');
  assert.equal(fixture.statuses.at(-1).kind, 'warn');
  assert.match(fixture.statuses.at(-1).message, /原生重命名成功.*列表刷新失败/);
});

test('closing mapped real id marks real closed before closing mapped token', () => {
  const fixture = makeFixture();
  fixture.state.realToNew.set('real', 'new-real');
  fixture.state.newToReal.set('new-real', 'real');
  fixture.terminalController.closeChecks = (token) => {
    assert.equal(token, 'new-real');
    assert.equal(fixture.state.closedTokens.has('real'), true);
  };
  fixture.controller.closeRealSession('real');
  assert.deepEqual(fixture.terminalController.closed, ['new-real']);
});

test('auto refresh skips unchanged signatures, guards reentry, and recovers after rejection', async () => {
  let resolveList;
  const first = [session('a')];
  const fixture = makeFixture({ listResults: [first, first, [session('a', 'work', 'Changed')], new Error('offline'), [session('b')]] });
  await fixture.controller.initialize();
  const firstRender = fixture.renderCount;
  await fixture.controller.autoRefreshSessions();
  assert.equal(fixture.renderCount, firstRender);

  const pending = new Promise((resolve) => { resolveList = resolve; });
  const concurrent = makeFixture();
  await concurrent.controller.initialize();
  const initialCalls = concurrent.listCalls.length;
  const initialRenders = concurrent.renderCount;
  concurrent.backend.ListSessions = async () => { concurrent.listCalls.push(true); return pending; };
  const firstRequest = concurrent.controller.autoRefreshSessions();
  const secondRequest = concurrent.controller.autoRefreshSessions();
  assert.equal(concurrent.listCalls.length - initialCalls, 1);
  resolveList([]);
  await Promise.all([firstRequest, secondRequest]);
  assert.equal(concurrent.renderCount, initialRenders);

  await fixture.controller.autoRefreshSessions();
  await assert.rejects(fixture.controller.autoRefreshSessions(), /offline/);
  await fixture.controller.autoRefreshSessions();
  assert.ok(fixture.renderCount > firstRender);
});

test('start is idempotent and stop clears one five-second timer', () => {
  const fixture = makeFixture();
  fixture.controller.start();
  fixture.controller.start();
  assert.deepEqual(fixture.intervals.map((timer) => timer.delay), [5000]);
  fixture.controller.stop();
  fixture.controller.stop();
  assert.equal(fixture.cleared.length, 1);
});

test('initialize continues restoring later open sessions after one failure', async () => {
  const started = [];
  const fixture = makeFixture({
    listResults: [[session('one'), session('two')]],
    GetOpenSessions: async () => ['missing', 'one', 'two'],
    StartSession: async (id) => {
      started.push(id);
      if (id === 'one') throw new Error('failed');
    },
  });
  await fixture.controller.initialize();
  assert.deepEqual(started, ['one', 'two']);
});

function iconTestElement(tag, className, text) {
  const node = new FakeNode();
  node.className = className;
  node.textContent = text || '';
  return node;
}

test('session rows combine provider and status on the left without a duplicate right icon', () => {
  const listRoot = new FakeNode();
  const state = createAppState();
  state.agentProviders.set('future', { name: '<Future Agent>' });
  renderSessionList({
    listRoot,
    list: [session('claude'), { ...session('codex'), provider: 'codex', temporary: true },
      { ...session('future'), provider: 'future' }],
    state, agentController: { classifyAgent: () => 'idle' }, el: iconTestElement,
    onStartNew() {}, onToggleGroup() {}, onOpen() {}, onClose() {}, onContextMenu() {},
  });
  const rows = listRoot.children[0].children[1].children;
  for (const [index, provider] of ['Claude', 'Codex'].entries()) {
    const nameRow = rows[index].children[0];
    assert.equal(nameRow.children.length, 3); // status, name, close
    const source = nameRow.children[0];
    assert.equal(source.attributes.get('role'), 'img');
    assert.match(source.attributes.get('aria-label'), new RegExp(provider + ' · 未打开'));
    assert.match(source.innerHTML, /<svg aria-hidden="true" focusable="false"/);
    assert.match(source.innerHTML, /fill="currentColor"/);
    assert.equal(nameRow.children[1].textContent, provider.toLowerCase());
    assert.equal(nameRow.children[2].textContent, '×');
  }
  assert.equal(rows[0].children[0].children[0].title, 'Claude · 未打开');
  assert.match(rows[1].children[0].children[0].title, /Codex.*临时终端，重启不自动恢复/);
  const fallback = rows[2].children[0].children[0];
  assert.equal(fallback.textContent, '●');
  assert.equal(fallback.title, '<Future Agent> · 未打开');
  assert.equal(fallback.innerHTML, '');
});

test('hidden sessions show product icons without changing names or restore targets', () => {
  const hiddenPanel = new FakeNode();
  const list = [session('claude'), { ...session('codex'), provider: 'codex' }];
  const restored = [];
  renderHiddenSessions({ hiddenPanel, list, el: iconTestElement,
    onRestore: (item) => restored.push(item) });
  for (const [index, provider] of ['Claude', 'Codex'].entries()) {
    const row = hiddenPanel.children[index];
    assert.equal(row.children[0].textContent, list[index].name);
    assert.equal(row.children[1].attributes.get('aria-label'), provider);
    row.children[3].click();
  }
  assert.deepEqual(restored, list);
});
