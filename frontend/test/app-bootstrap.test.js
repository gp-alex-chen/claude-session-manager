import test from 'node:test';
import assert from 'node:assert/strict';

import { createApplication } from '../src/app/bootstrap.js';

const REQUIRED_IDS = [
  'terminal', 'status-bar', 'status-message', 'project-bar', 'btn-add-project', 'session-list', 'hidden-panel', 'hidden-count',
  'btn-hidden', 'btn-eye', 'btn-settings', 'settings-menu', 'settings-dialog',
  'settings-close', 'settings-nav', 'settings-tab-appearance',
  'settings-tab-terminal', 'settings-tab-update', 'settings-content',
  'settings-panel-appearance', 'settings-panel-terminal', 'settings-panel-update',
  'settings-version',
];

class FakeNode {
  constructor(id = '') {
    this.id = id;
    this.children = [];
    this.parentNode = null;
    this.dataset = {};
    this.listeners = new Map();
    this.style = {};
    this.classList = {
      values: new Set(),
      add: (...names) => names.forEach((name) => this.classList.values.add(name)),
      remove: (...names) => names.forEach((name) => this.classList.values.delete(name)),
      toggle: (name, force) => {
        const next = force === undefined ? !this.classList.values.has(name) : force;
        if (next) this.classList.values.add(name);
        else this.classList.values.delete(name);
        return next;
      },
      forEach: (callback) => this.classList.values.forEach(callback),
    };
    this.className = '';
    this.textContent = '';
    this.innerHTML = '';
    this.hidden = false;
    this.attributes = new Map();
  }
  append(...children) { children.forEach((child) => this.appendChild(child)); }
  appendChild(child) { child.parentNode = this; this.children.push(child); return child; }
  replaceChildren(...children) { this.children = children; children.forEach((child) => { child.parentNode = this; }); }
  addEventListener(name, callback) { this.listeners.set(name, callback); }
  removeEventListener(name, callback) {
    if (this.listeners.get(name) === callback) this.listeners.delete(name);
  }
  setAttribute(name, value) { this.attributes.set(name, String(value)); }
  getAttribute(name) { return this.attributes.get(name); }
}

function makeDocument(missing) {
  const elements = new Map(REQUIRED_IDS
    .filter((id) => id !== missing)
    .map((id) => [id, new FakeNode(id)]));
  const documentRef = {
    body: new FakeNode('body'),
    documentElement: new FakeNode('html'),
    getElementById: (id) => elements.get(id) || null,
    createElement: (tag) => new FakeNode(tag),
    createTextNode: (text) => ({ nodeType: 3, textContent: text }),
  };
  return { documentRef, elements };
}

function makeFixture(options = {}) {
  const { documentRef, elements } = makeDocument(options.missing);
  const windowListeners = new Map();
  const animationFrames = [];
  const windowRef = {
    addEventListener: (name, callback) => windowListeners.set(name, callback),
    removeEventListener: (name, callback) => {
      if (windowListeners.get(name) === callback) windowListeners.delete(name);
    },
    requestAnimationFrame: (callback) => {
      animationFrames.push(callback);
      return animationFrames.length;
    },
  };
  if (options.storageError) {
    Object.defineProperty(windowRef, 'localStorage', {
      get() { throw new Error('storage unavailable'); },
    });
  }

  const runtimeListeners = new Map();
  const eventsOff = [];
  const runtime = {
    EventsOn: (name, callback) => {
      runtimeListeners.set(name, callback);
      if (options.cancelEvents) {
        return () => runtimeListeners.delete(name);
      }
      return undefined;
    },
    EventsOff: (name) => {
      eventsOff.push(name);
      runtimeListeners.delete(name);
    },
    ClipboardSetText: async (text) => { calls.clipboardWrites.push(text); },
    ClipboardGetText: async () => {
      calls.clipboardReads += 1;
      return 'runtime clipboard';
    },
  };
  const calls = {
    factories: {}, starts: {}, stops: {}, initializes: {}, routed: [],
    resize: 0, cancels: 0, clipboardReads: 0, clipboardWrites: [],
  };
  const makeController = (name, extra = {}) => {
    calls.starts[name] = 0;
    calls.stops[name] = 0;
    calls.initializes[name] = 0;
    return {
      start: () => { calls.starts[name] += 1; },
      stop: () => { calls.stops[name] += 1; },
      initialize: () => {
        calls.initializes[name] += 1;
        if (options.rejectInitialize === name) return Promise.reject(new Error(name + ' failed'));
        return Promise.resolve();
      },
      ...extra,
    };
  };
  const factories = {
    agent: (deps) => {
      calls.factories.agent = deps;
      return makeController('agent', {
        applyAgents: (value) => calls.routed.push(['agents', value]),
        renderUnreadMarks: () => {},
        showToast: (value) => calls.routed.push(['toast', value]),
      });
    },
    terminal: (deps) => {
      calls.factories.terminal = deps;
      return makeController('terminal', {
        handleData: (...value) => calls.routed.push(['data', value]),
        handleExit: (...value) => calls.routed.push(['exit', value]),
        resizeActive: () => { calls.resize += 1; },
        resizeVisible: () => { calls.resize += 1; },
      });
    },
    session: (deps) => {
      calls.factories.session = deps;
      return makeController('session', {
        refreshFoldState: () => calls.routed.push(['fold']),
        syncActiveHighlight: () => calls.routed.push(['active']),
        handleTerminalExit: (...value) => calls.routed.push(['session-exit', value]),
      });
    },
    settings: (deps) => {
      calls.factories.settings = deps;
      return makeController('settings');
    },
    update: (deps) => {
      calls.factories.update = deps;
      return {
        handleState: (value) => calls.routed.push(['update-state', value]),
        handleProgress: (value) => calls.routed.push(['update-progress', value]),
      };
    },
    usage: (deps) => {
      calls.factories.usage = deps;
      return makeController('usage', {
        onActivate: (token) => calls.routed.push(['usage-activate', token]),
        refreshActive: () => calls.routed.push(['usage-refresh']),
        refreshVisible: (options) => calls.routed.push([
          'usage-visible',
          options,
          deps.getVisibleAssignments?.(),
        ]),
        refreshToken: (token, options) => calls.routed.push(['usage-token', token, options]),
        removeToken: (token) => calls.routed.push(['usage-remove', token]),
      });
    },
  };
  const backend = new Proxy({}, { get: () => () => {} });
  const app = createApplication({
    documentRef,
    windowRef,
    runtime,
    backend,
    TerminalCtor: class {},
    FitAddonCtor: class {},
    controllerFactories: factories,
    onError: options.onError,
  });
  return {
    app,
    calls,
    documentRef,
    elements,
    windowListeners,
    runtimeListeners,
    eventsOff,
    animationFrames,
  };
}

test('bootstrap creates controllers around one shared state and wires callbacks', async () => {
  const fixture = makeFixture();
  assert.equal(Object.keys(fixture.calls.factories).length, 6);
  assert.equal(fixture.calls.factories.agent.state, fixture.app.state);
  assert.equal(fixture.calls.factories.session.state, fixture.app.state);
  assert.equal(fixture.calls.factories.usage.state, fixture.app.state);
  assert.deepEqual(fixture.app.state.projects, []);
  assert.equal(typeof fixture.calls.factories.usage.GetUsageSummary, 'function');
  assert.equal(Array.isArray(fixture.calls.factories.usage.surfaces), true);
  assert.equal(typeof fixture.calls.factories.session.onPair, 'function');
  fixture.calls.factories.agent.setStatus('临时消息', 'ok');
  assert.equal(fixture.elements.get('status-message').textContent, '临时消息');
  fixture.calls.factories.agent.refreshFoldState();
  fixture.calls.factories.terminal.onActivate();
  await fixture.calls.factories.terminal.writeClipboard('复制内容');
  assert.equal(await fixture.calls.factories.terminal.readClipboard(), 'runtime clipboard');
  fixture.calls.factories.terminal.requestFrame(() => fixture.calls.routed.push(['frame']));
  fixture.animationFrames.shift()();
  fixture.calls.factories.update.showToast('hello');
  assert.deepEqual(fixture.calls.routed, [['fold'], ['usage-activate', null], ['active'], ['frame'], ['toast', 'hello']]);
  assert.deepEqual(fixture.calls.clipboardWrites, ['复制内容']);
  assert.equal(fixture.calls.clipboardReads, 1);
});

test('bootstrap passes project APIs and the top project add button to the session controller', () => {
  const fixture = makeFixture();
  const sessionDeps = fixture.calls.factories.session;

  assert.ok(fixture.elements.get('project-bar'));
  assert.equal(fixture.calls.factories.session.projectRoot, undefined);
  assert.equal(sessionDeps.addProjectButton, fixture.elements.get('btn-add-project'));
  for (const name of ['ListProjects', 'ChooseProjectDir', 'AddProject', 'AdoptSession']) {
    assert.equal(typeof sessionDeps.backend[name], 'function', name);
  }
});

test('bootstrap refreshes visible usage after pane initialization and routes pairing/dispose', async () => {
  const fixture = makeFixture();
  fixture.app.state.panes[0].token = 'new-1';
  await fixture.app.start();

  assert.deepEqual(fixture.calls.routed.find(([name]) => name === 'usage-visible'), [
    'usage-visible',
    { force: false },
    [{ id: 'pane-0', token: 'new-1' }],
  ]);
  fixture.calls.factories.session.onPair({ token: 'new-1' });
  fixture.calls.factories.terminal.onDispose('new-1');
  assert.deepEqual(fixture.calls.routed.find(([name]) => name === 'usage-token'), [
    'usage-token', 'new-1', { force: true },
  ]);
  assert.deepEqual(fixture.calls.routed.slice(-2), [
    ['usage-visible', { force: false }, []],
    ['usage-remove', 'new-1'],
  ]);
});

test('start is idempotent and initializes session/settings once', async () => {
  const fixture = makeFixture();
  const first = fixture.app.start();
  const second = fixture.app.start();
  assert.equal(first, second);
  assert.equal(fixture.runtimeListeners.size, 5);
  assert.equal(fixture.windowListeners.size, 1);
  assert.deepEqual(fixture.calls.starts, { agent: 1, terminal: 0, session: 1, settings: 1, usage: 1 });
  await first;
  assert.deepEqual(fixture.calls.initializes, { agent: 0, terminal: 0, session: 1, settings: 1, usage: 0 });
});

test('runtime and resize events route to the matching controllers', () => {
  const fixture = makeFixture();
  fixture.app.start();
  fixture.runtimeListeners.get('agents:update')(['agent']);
  fixture.runtimeListeners.get('term:data')('token', 'b64');
  fixture.runtimeListeners.get('term:exit')('token');
  fixture.calls.factories.terminal.onExit('token');
  fixture.runtimeListeners.get('update:state')('下载中');
  fixture.runtimeListeners.get('update:progress')(42);
  fixture.windowListeners.get('resize')();
  assert.deepEqual(fixture.calls.routed, [
    ['usage-visible', { force: false }, []],
    ['agents', ['agent']], ['data', ['token', 'b64']], ['exit', ['token']],
    ['session-exit', ['token']],
    ['usage-visible', { force: false }, []],
    ['update-state', '下载中'], ['update-progress', 42],
  ]);
  assert.equal(fixture.calls.resize, 1);
});

test('stop is idempotent and uses returned event cancellation functions', () => {
  const fixture = makeFixture({ cancelEvents: true });
  fixture.app.start();
  fixture.app.stop();
  fixture.app.stop();
  assert.deepEqual(fixture.calls.stops, { agent: 1, terminal: 0, session: 1, settings: 1, usage: 1 });
  assert.equal(fixture.windowListeners.size, 0);
  assert.equal(fixture.runtimeListeners.size, 0);
  assert.deepEqual(fixture.eventsOff, []);
});

test('stop falls back to EventsOff when EventsOn returns no cancel function', () => {
  const fixture = makeFixture();
  fixture.app.start();
  fixture.app.stop();
  assert.deepEqual(fixture.eventsOff, [
    'update:progress', 'update:state', 'term:exit', 'term:data', 'agents:update',
  ]);
});

test('stop permits a clean second start and fresh initialization', async () => {
  const fixture = makeFixture();
  fixture.app.start();
  fixture.app.stop();
  const ready = fixture.app.start();
  await ready;
  assert.deepEqual(fixture.calls.starts, { agent: 2, terminal: 0, session: 2, settings: 2, usage: 2 });
  assert.deepEqual(fixture.calls.initializes, { agent: 0, terminal: 0, session: 2, settings: 2, usage: 0 });
  assert.equal(fixture.runtimeListeners.size, 5);
});

test('initializer failure is contained while the other initializer still runs', async () => {
  const fixture = makeFixture({ rejectInitialize: 'session' });
  await assert.doesNotReject(fixture.app.start());
  assert.deepEqual(fixture.calls.initializes, { agent: 0, terminal: 0, session: 1, settings: 1, usage: 0 });
});

test('missing required DOM ids fail fast with the missing id', () => {
  for (const id of REQUIRED_IDS) {
    assert.throws(() => makeFixture({ missing: id }), new RegExp('#' + id));
  }
});

test('status messages are inserted as text, not HTML', async () => {
  const fixture = makeFixture({ rejectInitialize: 'session' });
  await fixture.app.start();
  const status = fixture.elements.get('status-message');
  assert.equal(status.innerHTML, '');
  assert.match(status.textContent, /初始化失败/);
});

test('storage access errors do not prevent settings construction', async () => {
  const fixture = makeFixture({ storageError: true });
  await assert.doesNotReject(fixture.app.start());
  assert.equal(fixture.calls.initializes.settings, 1);
});
