import test from 'node:test';
import assert from 'node:assert/strict';

import { createAppState } from '../src/state/app-state.js';
import { createSessionController } from '../src/sessions/controller.js';
import { listSig, pairPendingSessions } from '../src/sessions/pairing.js';
import { renderSessionList } from '../src/sessions/view.js';

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
    this.innerHTML = '';
    this.textContent = '';
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
  setAttribute() {}
}

function makeFixture(options = {}) {
  const state = createAppState();
  const listRoot = new FakeNode();
  const addProjectButton = new FakeNode();
  let renderCount = 0;
  Object.defineProperty(listRoot, 'innerHTML', {
    get() { return ''; },
    set() { renderCount += 1; },
  });
  const hiddenPanel = new FakeNode();
  const hiddenCount = new FakeNode();
  const hiddenButton = new FakeNode();
  const eyeButton = new FakeNode();
  const documentRef = {
    body: new FakeNode(),
    createElement: () => new FakeNode(),
    addEventListener() {},
  };
  const windowRef = {
    innerWidth: 1000,
    innerHeight: 800,
    prompt: () => 'Renamed',
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
  const adoptionCalls = [];
  let projectIndex = 0;
  const listResults = options.listResults || [[]];
  const projectResults = options.projectResults || [[]];
  const backend = {
    ListSessions: async () => {
      listCalls.push(true);
      if (options.listWait) return options.listWait;
      const result = listResults[Math.min(listIndex++, listResults.length - 1)];
      if (result instanceof Error) throw result;
      return result;
    },
    ListHiddenSessions: async () => [],
    RenameSession: options.RenameSession || (async () => {}),
    DeleteSession: options.DeleteSession || (async () => {}),
    UnhideSession: async () => {},
    StartSession: options.StartSession || (async () => {}),
    StartNew: options.StartNew || (async () => 'new-1'),
    AdoptSession: options.AdoptSession || (async (token, realId) => {
      adoptionCalls.push([token, realId]);
    }),
    GetOpenSessions: options.GetOpenSessions || (async () => { openCalls.push(true); return []; }),
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
    addProjectButton,
    hiddenPanel,
    hiddenCount,
    hiddenButton,
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
    adoptionCalls,
    paneController,
    get renderCount() { return renderCount; },
  };
}

const session = (id, dir = 'work', name = id, time = 'today') => ({ id, dir, name, time });

function renderProjectGroups(projects, list, onStartNew = () => {}, onToggleGroup = () => {}) {
  const listRoot = new FakeNode();
  renderSessionList({
    listRoot,
    projects,
    list,
    state: createAppState(),
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
    target: { type: 'session', id: 'session-1', dir: 'work', name: 'session-1' },
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
  await fixture.controller.autoRefreshSessions();
  const firstRender = fixture.renderCount;
  await fixture.controller.autoRefreshSessions();
  assert.equal(fixture.renderCount, firstRender);

  const pending = new Promise((resolve) => { resolveList = resolve; });
  const concurrent = makeFixture({ listWait: pending });
  const firstRequest = concurrent.controller.autoRefreshSessions();
  const secondRequest = concurrent.controller.autoRefreshSessions();
  assert.equal(concurrent.listCalls.length, 1);
  resolveList([]);
  await Promise.all([firstRequest, secondRequest]);
  assert.equal(concurrent.renderCount, 1);

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
