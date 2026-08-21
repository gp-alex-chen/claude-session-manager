import test from 'node:test';
import assert from 'node:assert/strict';

import { createAppState } from '../src/state/app-state.js';
import { createTerminalController } from '../src/terminal/controller.js';
import { b64ToBytes, bytesToB64 } from '../src/utils.js';
import { createTermOptions, THEMES } from '../src/themes/catalog.js';

class FakeClassList {
  constructor() {
    this.values = new Set();
  }

  add(...names) {
    names.forEach((name) => this.values.add(name));
  }

  contains(name) {
    return this.values.has(name);
  }

  toggle(name, enabled) {
    if (enabled) this.values.add(name);
    else this.values.delete(name);
  }
}

class FakeHost {
  constructor() {
    this.classList = new FakeClassList();
    this.removed = false;
    this.listeners = new Map();
  }

  addEventListener(name, callback) { this.listeners.set(name, callback); }
  emit(name, event) { return this.listeners.get(name)?.(event); }

  remove() {
    this.removed = true;
  }
}

class FakeFit {
  fit() {}
}

class FakeTerm {
  constructor(options) {
    this.options = options;
    this.cols = 80;
    this.rows = 24;
    this.writes = [];
    this.pastes = [];
    this.disposed = false;
    this.selection = '';
  }

  loadAddon(addon) { this.addon = addon; }
  open(host) { this.host = host; }
  resize(cols, rows) { this.cols = cols; this.rows = rows; }
  onData(handler) { this.dataHandler = handler; }
  onResize(handler) { this.resizeHandler = handler; }
  attachCustomKeyEventHandler(handler) { this.keyHandler = handler; }
  write(bytes) { this.writes.push(bytes); }
  paste(text) { this.pastes.push(text); }
  hasSelection() { return this.selection.length > 0; }
  getSelection() { return this.selection; }
  focus() { this.focused = true; }
  dispose() { this.disposed = true; }
  emitData(data) { this.dataHandler(data); }
  emitKey(event) { return this.keyHandler(event); }
}

function createFixture(options = {}) {
  const state = createAppState();
  const hosts = [];
  const writes = [];
  const killed = [];
  const statuses = [];
  const copied = [];
  const documentRef = {
    documentElement: { style: { setProperty() {} } },
  };
  const navigatorRef = { clipboard: { readText: async () => '粘贴内容' } };
  const controller = createTerminalController({
    state,
    backend: {
      TermWrite: (token, b64) => writes.push({ token, b64 }),
      TermResize: () => {},
      TermKill: (token) => {
        assert.equal(state.closedTokens.has(token), true);
        killed.push(token);
      },
    },
    TerminalCtor: FakeTerm,
    FitAddonCtor: FakeFit,
    termOptions: createTermOptions(),
    themes: THEMES,
    setStatus: (message, kind) => statuses.push({ message, kind }),
    hostFactory: () => {
      const host = new FakeHost();
      hosts.push(host);
      return host;
    },
    appendHost: () => {},
    documentRef,
    navigatorRef,
    storageRef: { setItem() {} },
    writeClipboard: options.writeClipboard || (async (text) => { copied.push(text); }),
  });
  return { state, controller, hosts, writes, killed, statuses, copied };
}

function openAndActivate(fixture, token = 'session-1') {
  fixture.controller.openTab(token, token);
  fixture.controller.activate(token);
  return fixture.state.terminals.get(token);
}

test('closed data is ignored while unknown open data creates a terminal', () => {
  const fixture = createFixture();
  fixture.state.closedTokens.add('closed');
  fixture.controller.handleData('closed', bytesToB64(new TextEncoder().encode('late')));
  assert.equal(fixture.state.terminals.size, 0);

  const payload = bytesToB64(new TextEncoder().encode('hello'));
  fixture.controller.handleData('unknown', payload);
  const session = fixture.state.terminals.get('unknown');
  assert.ok(session?.term);
  assert.deepEqual(new TextDecoder().decode(session.term.writes[0]), 'hello');
});

test('close marks closed before killing and clears new-session mappings', () => {
  const fixture = createFixture();
  openAndActivate(fixture, 'new-1');
  fixture.state.pendingNew.push({ token: 'new-1', dir: 'work' });
  fixture.state.newToReal.set('new-1', 'real-1');
  fixture.state.realToNew.set('real-1', 'new-1');

  fixture.controller.closeTab('new-1');
  assert.deepEqual(fixture.killed, ['new-1']);
  assert.equal(fixture.state.closedTokens.has('new-1'), true);
  assert.equal(fixture.state.pendingNew.length, 0);
  assert.equal(fixture.state.newToReal.has('new-1'), false);
  assert.equal(fixture.state.realToNew.has('real-1'), false);
  assert.equal(fixture.state.terminals.has('new-1'), false);
});

test('late data and exit after close cannot resurrect a terminal', () => {
  const fixture = createFixture();
  openAndActivate(fixture, 'session-1');
  fixture.controller.closeTab('session-1');
  fixture.controller.handleData('session-1', bytesToB64(new Uint8Array([1])));
  fixture.controller.handleExit('session-1');
  assert.equal(fixture.state.terminals.has('session-1'), false);
  assert.equal(fixture.hosts[0].removed, true);
});

test('activate updates active token and clears unread state', () => {
  const fixture = createFixture();
  fixture.state.unreadSessions.add('session-1');
  const session = openAndActivate(fixture, 'session-1');
  assert.equal(fixture.state.activeToken, 'session-1');
  assert.equal(fixture.state.unreadSessions.has('session-1'), false);
  assert.equal(session.term.focused, true);
  assert.equal(session.host.classList.values.has('active'), true);
});

test('multiple terminals use overlapping hosts with only the active host shown', () => {
  const fixture = createFixture();
  const first = openAndActivate(fixture, 'session-1');
  const second = fixture.controller.openTab('session-2', 'session-2');
  fixture.controller.activate('session-2');

  assert.equal(first.host.classList.contains('term-host'), true);
  assert.equal(second.host.classList.contains('term-host'), true);
  assert.equal(first.host.classList.contains('active'), false);
  assert.equal(second.host.classList.contains('active'), true);
});

test('applyTheme updates options on existing terminals', () => {
  const fixture = createFixture();
  const session = openAndActivate(fixture, 'session-1');
  fixture.controller.applyTheme('dracula');
  assert.equal(fixture.state.currentTheme, 'dracula');
  assert.equal(session.term.options.theme, THEMES.dracula);
});

test('terminal input preserves UTF-8 and shortcut semantics', async () => {
  const fixture = createFixture();
  const session = openAndActivate(fixture, 'session-1');
  session.term.emitData('中文');
  assert.equal(new TextDecoder().decode(b64ToBytes(fixture.writes[0].b64)), '中文');

  const event = (key, extras = {}) => ({
    type: 'keydown', key, ctrlKey: false, metaKey: false, shiftKey: false,
    preventDefault() { this.prevented = true; }, ...extras,
  });
  assert.equal(session.term.emitKey(event('v', { ctrlKey: true })), false);
  assert.equal(session.term.emitKey(event('v', { ctrlKey: true, shiftKey: true })), false);
  assert.equal(session.term.emitKey(event('Insert', { shiftKey: true })), false);
  assert.equal(session.term.emitKey(event('Enter', { ctrlKey: true })), false);
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(session.term.pastes, ['粘贴内容', '粘贴内容', '粘贴内容']);
  assert.equal(new TextDecoder().decode(b64ToBytes(fixture.writes.at(-1).b64)), '\n');
});

test('terminal right click copies one complete UTF-8 multiline selection', async () => {
  const fixture = createFixture();
  const session = openAndActivate(fixture, 'session-1');
  session.term.selection = '第一行🙂\nsecond line\n第三行';
  const event = {
    preventDefault() { this.prevented = (this.prevented || 0) + 1; },
    stopPropagation() { this.stopped = (this.stopped || 0) + 1; },
  };

  session.host.emit('contextmenu', event);
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(event.prevented, 1);
  assert.equal(event.stopped, 1);
  assert.deepEqual(fixture.copied, ['第一行🙂\nsecond line\n第三行']);
  assert.deepEqual(fixture.statuses.at(-1), { message: '已复制', kind: 'ok' });
});

test('terminal right click without a selection does not write or report success', async () => {
  const fixture = createFixture();
  const session = openAndActivate(fixture, 'session-1');
  const event = {
    preventDefault() { this.prevented = true; },
    stopPropagation() { this.stopped = true; },
  };

  session.host.emit('contextmenu', event);
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(event.prevented, true);
  assert.equal(event.stopped, true);
  assert.deepEqual(fixture.copied, []);
  assert.equal(fixture.statuses.some(({ message }) => message === '已复制'), false);
});

test('terminal right click reports clipboard failures without leaking rejection', async () => {
  const fixture = createFixture({
    writeClipboard: async () => { throw new Error('clipboard denied'); },
  });
  const session = openAndActivate(fixture, 'session-1');
  session.term.selection = 'copy me';
  const event = { preventDefault() {}, stopPropagation() {} };

  await assert.doesNotReject(async () => {
    session.host.emit('contextmenu', event);
    await new Promise((resolve) => setImmediate(resolve));
  });
  assert.deepEqual(fixture.statuses.at(-1), {
    message: '复制失败: Error: clipboard denied',
    kind: 'warn',
  });
});
