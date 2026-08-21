import test from 'node:test';
import assert from 'node:assert/strict';

import { createAppState } from '../src/state/app-state.js';
import { createSessionController } from '../src/sessions/controller.js';
import { listSig, pairPendingSessions } from '../src/sessions/pairing.js';

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
    this.dataset = {};
    this.style = { setProperty() {} };
    this.classList = new FakeClassList();
    this.listeners = new Map();
    this.innerHTML = '';
    this.textContent = '';
  }
  append(...children) { this.children.push(...children); }
  appendChild(child) { this.children.push(child); return child; }
  addEventListener(name, callback) { this.listeners.set(name, callback); }
  querySelectorAll() { return []; }
  querySelector() { return null; }
  remove() { this.removed = true; }
  setAttribute() {}
}

function makeFixture(options = {}) {
  const state = createAppState();
  const listRoot = new FakeNode();
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
    confirm: () => true,
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
  const agentController = {
    classifyAgent: () => 'idle',
    refreshAgents: async () => {},
    renderUnreadMarks: () => {},
  };
  const statuses = [];
  let listIndex = 0;
  const listCalls = [];
  const listResults = options.listResults || [[]];
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
    GetOpenSessions: options.GetOpenSessions || (async () => []),
  };
  const intervals = [];
  const cleared = [];
  const controller = createSessionController({
    state,
    backend,
    terminalController,
    agentController,
    listRoot,
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
    setIntervalFn: (callback, delay) => {
      const timer = { callback, delay };
      intervals.push(timer);
      return timer;
    },
    clearIntervalFn: (timer) => cleared.push(timer),
  });
  return {
    state, controller, backend, terminals, statuses, intervals, cleared, listCalls,
    listRoot, terminalController, get renderCount() { return renderCount; },
  };
}

const session = (id, dir = 'work', name = id, time = 'today') => ({ id, dir, name, time });

test('listSig ignores time but tracks id, directory, and name', () => {
  const first = [session('a', 'work', 'A', 'one')];
  assert.equal(listSig(first), listSig([session('a', 'work', 'A', 'two')]));
  assert.notEqual(listSig(first), listSig([session('b', 'work', 'A')]));
  assert.notEqual(listSig(first), listSig([session('a', 'other', 'A')]));
  assert.notEqual(listSig(first), listSig([session('a', 'work', 'B')]));
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
  assert.equal(fixture.state.unreadSessions.has('running'), false);

  const exited = fixture.terminalController.openTab('bad', 'Bad');
  exited.exited = true;
  await fixture.controller.openFromList(session('bad'));
  assert.deepEqual(fixture.terminalController.disposed, ['bad', 'bad']);
  assert.equal(fixture.state.terminals.has('bad'), false);

  await fixture.controller.openFromList(session('good'));
  assert.equal(fixture.state.terminals.has('good'), true);
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

test('controller pairing updates the temporary label for a new real session', () => {
  const fixture = makeFixture();
  fixture.controller.renderSessions([session('old', 'work', 'Old')]);
  fixture.state.activeToken = 'new-1';
  fixture.state.pendingNew.push({ token: 'new-1', dir: 'work' });
  fixture.terminalController.openTab('new-1', '新会话 1');

  fixture.controller.pairNewSessions([
    session('old', 'work', 'Old'),
    session('real', 'work', 'Real name'),
  ]);

  assert.equal(fixture.state.realToNew.get('real'), 'new-1');
  assert.equal(fixture.state.terminals.get('new-1').labelText, 'Real name');
  assert.deepEqual(fixture.state.pendingNew, []);
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
