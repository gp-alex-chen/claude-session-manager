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
    this.children = [];
    this.parentNode = null;
    this.removed = false;
    this.listeners = new Map();
  }

  addEventListener(name, callback) { this.listeners.set(name, callback); }
  emit(name, event) { return this.listeners.get(name)?.(event); }

  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    return child;
  }

  remove() {
    this.removed = true;
  }
}

class FakeFit {
  constructor() { this.fitCalls = 0; }
  fit() {
    this.fitCalls += 1;
    if (this.term) {
      this.term.resize(80, 24);
    }
  }
}

class FakeTerm {
  constructor(options) {
    this.options = { ...options };
    this.cols = 80;
    this.rows = 24;
    this.writes = [];
    this.pastes = [];
    this.disposed = false;
    this.selection = '';
    this.clearSelectionCalls = 0;
  }

  loadAddon(addon) { this.addon = addon; addon.term = this; }
  open(host) { this.host = host; }
  resize(cols, rows) {
    this.cols = cols;
    this.rows = rows;
    this.resizeHandler?.();
  }
  onData(handler) { this.dataHandler = handler; }
  onResize(handler) { this.resizeHandler = handler; }
  attachCustomKeyEventHandler(handler) { this.keyHandler = handler; }
  write(bytes) { this.writes.push(bytes); }
  paste(text) {
    this.pastes.push(text);
    if (this.options.pasteEmitsData) this.emitData(text);
  }
  getSelection() { return this.selection; }
  clearSelection() { this.selection = ''; this.clearSelectionCalls += 1; }
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
  const resizes = [];
  const statuses = [];
  const clipboardReads = [];
  const clipboardWrites = [];
  const exits = [];
  const storageWrites = [];
  const frames = [];
  const termOptions = { ...createTermOptions(), pasteEmitsData: options.pasteEmitsData };
  const documentRef = {
    documentElement: { style: { setProperty() {} } },
  };
  const navigatorRef = { clipboard: { readText: async () => '粘贴内容' } };
  const controller = createTerminalController({
    state,
    backend: {
      TermWrite: (token, b64) => writes.push({ token, b64 }),
      TermResize: (token, cols, rows) => resizes.push({ token, cols, rows }),
      TermKill: (token) => {
        assert.equal(state.closedTokens.has(token), true);
        killed.push(token);
      },
    },
    TerminalCtor: FakeTerm,
    FitAddonCtor: FakeFit,
    termOptions,
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
    storageRef: {
      setItem(key, value) {
        if (options.storageSetError) throw new Error('storage unavailable');
        storageWrites.push([key, value]);
      },
    },
    layoutManaged: options.layoutManaged === true,
    readClipboard: options.readClipboard || (async () => {
      clipboardReads.push(true);
      return options.clipboardText ?? '粘贴内容';
    }),
    writeClipboard: options.writeClipboard || (async (text) => {
      clipboardWrites.push(text);
    }),
    requestFrame: (callback) => {
      frames.push(callback);
      return frames.length;
    },
    onExit: (token) => exits.push(token),
    onDispose: options.onDispose,
  });
  return {
    state,
    controller,
    hosts,
    writes,
    killed,
    resizes,
    statuses,
    clipboardReads,
    clipboardWrites,
    storageWrites,
    frames,
    exits,
    termOptions,
    flushFrame: () => frames.shift()?.(),
  };
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
  assert.deepEqual(fixture.exits, ['session-1']);
});

test('exit callback runs even when the terminal was already marked closed', () => {
  const fixture = createFixture();
  fixture.state.closedTokens.add('closed');
  fixture.controller.handleExit('closed');
  assert.deepEqual(fixture.exits, ['closed']);
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

test('layout-managed terminals can stay mounted together and focus does not hide siblings', () => {
  const fixture = createFixture({ layoutManaged: true, onDispose: () => {} });
  const first = fixture.controller.openTab('first', 'first');
  const second = fixture.controller.openTab('second', 'second');
  fixture.controller.mountSession('first', new FakeHost());
  fixture.controller.mountSession('second', new FakeHost());

  fixture.controller.activate('second');
  assert.equal(first.visible, true);
  assert.equal(second.visible, true);
  assert.equal(first.host.classList.contains('is-mounted'), true);
  assert.equal(second.host.classList.contains('is-mounted'), true);
  assert.equal(fixture.state.activeToken, 'second');
});

test('layout-managed resize and font fitting cover every visible terminal with deduped sizes', () => {
  const fixture = createFixture({ layoutManaged: true, onDispose: () => {} });
  const first = fixture.controller.openTab('first', 'first');
  const second = fixture.controller.openTab('second', 'second');
  fixture.controller.mountSession('first', new FakeHost());
  fixture.controller.mountSession('second', new FakeHost());
  fixture.flushFrame();
  const resizes = fixture.resizes.length;

  fixture.controller.resizeVisible(['first', 'second']);
  assert.equal(fixture.resizes.length, resizes);
  const firstFits = first.fit.fitCalls;
  const secondFits = second.fit.fitCalls;
  fixture.controller.applyFontSize(18, false);
  fixture.flushFrame();
  assert.equal(first.fit.fitCalls, firstFits + 1);
  assert.equal(second.fit.fitCalls, secondFits + 1);
  assert.equal(fixture.resizes.length, resizes);
});

test('applyTheme updates options on existing terminals', () => {
  const fixture = createFixture();
  const session = openAndActivate(fixture, 'session-1');
  fixture.controller.applyTheme('dracula');
  assert.equal(fixture.state.currentTheme, 'dracula');
  assert.equal(session.term.options.theme, THEMES.dracula);
});

test('applyFontSize updates active and hidden terminals while new terminals inherit it', () => {
  const fixture = createFixture();
  const active = openAndActivate(fixture, 'active');
  const hidden = fixture.controller.openTab('hidden', 'hidden');
  fixture.controller.makeTerminal(hidden);
  const activeFits = active.fit.fitCalls;
  const hiddenFits = hidden.fit.fitCalls;
  const resizeCount = fixture.resizes.length;

  assert.equal(fixture.controller.applyFontSize(18.4), 18);
  assert.equal(fixture.controller.getFontSize(), 18);
  assert.equal(fixture.state.terminalFontSize, 18);
  assert.equal(fixture.termOptions.fontSize, 18);
  assert.equal(active.term.options.fontSize, 18);
  assert.equal(hidden.term.options.fontSize, 18);
  assert.equal(fixture.frames.length, 1);
  assert.equal(active.fit.fitCalls, activeFits);
  assert.equal(hidden.fit.fitCalls, hiddenFits);

  fixture.flushFrame();
  assert.equal(active.fit.fitCalls, activeFits + 1);
  assert.equal(hidden.fit.fitCalls, hiddenFits);
  assert.equal(fixture.resizes.length, resizeCount);
  assert.deepEqual(fixture.resizes.at(-1), { token: 'active', cols: 79, rows: 24 });

  const future = fixture.controller.openTab('future', 'future');
  fixture.controller.makeTerminal(future);
  assert.equal(future.term.options.fontSize, 18);
  assert.match(fixture.statuses.at(-1).message, /18px/);
});

test('applyFontSize normalizes values and coalesces active fits within one frame', () => {
  const fixture = createFixture();
  const session = openAndActivate(fixture, 'active');
  const fitCalls = session.fit.fitCalls;
  const focusState = session.term.focused;

  assert.equal(fixture.controller.getFontSize(), 14);
  fixture.controller.applyFontSize(Number.NaN, false);
  fixture.controller.applyFontSize(100, false);
  fixture.controller.applyFontSize(19.6, false);
  assert.equal(fixture.controller.getFontSize(), 20);
  assert.equal(fixture.frames.length, 1);

  fixture.flushFrame();
  assert.equal(session.fit.fitCalls, fitCalls + 1);
  assert.equal(session.term.focused, focusState);
});

test('applyFontSize persists normalized values even without notification', () => {
  const fixture = createFixture();
  openAndActivate(fixture, 'active');

  assert.equal(fixture.controller.applyFontSize(19.6, false), 20);
  assert.deepEqual(fixture.storageWrites.at(-1), ['term-font-size', '20']);

  const brokenStorage = createFixture({ storageSetError: true });
  openAndActivate(brokenStorage, 'active');
  assert.doesNotThrow(() => brokenStorage.controller.applyFontSize('invalid', false));
  assert.equal(brokenStorage.controller.getFontSize(), 14);
});

test('scheduled font fit ignores stale sessions after switching or closing', () => {
  const switched = createFixture();
  const first = openAndActivate(switched, 'first');
  const second = switched.controller.openTab('second', 'second');
  switched.controller.makeTerminal(second);
  switched.controller.applyFontSize(18, false);
  const firstFits = first.fit.fitCalls;
  switched.controller.activate('second');
  const secondFits = second.fit.fitCalls;
  switched.flushFrame();
  assert.equal(first.fit.fitCalls, firstFits);
  assert.equal(second.fit.fitCalls, secondFits);

  switched.controller.applyFontSize(19, false);
  switched.controller.applyFontSize(20, false);
  assert.equal(switched.frames.length, 1);
  switched.flushFrame();
  assert.equal(first.fit.fitCalls, firstFits);
  assert.equal(second.fit.fitCalls, secondFits + 1);

  const closed = createFixture();
  const session = openAndActivate(closed, 'closing');
  closed.controller.applyFontSize(17, false);
  const fits = session.fit.fitCalls;
  closed.controller.closeTab('closing');
  assert.doesNotThrow(() => closed.flushFrame());
  assert.equal(session.fit.fitCalls, fits);
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

const contextEvent = () => ({
  preventDefault() { this.prevented = (this.prevented || 0) + 1; },
  stopPropagation() { this.stopped = (this.stopped || 0) + 1; },
});

test('right click copies one UTF-8 multiline selection and clears it after success', async () => {
  const fixture = createFixture();
  const session = openAndActivate(fixture, 'session-1');
  session.term.selection = '第一行🙂\nsecond line\n第三行';
  const event = contextEvent();

  session.host.emit('contextmenu', event);
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(event.prevented, 1);
  assert.equal(event.stopped, 1);
  assert.deepEqual(fixture.clipboardWrites, ['第一行🙂\nsecond line\n第三行']);
  assert.equal(fixture.clipboardReads.length, 0);
  assert.deepEqual(session.term.pastes, []);
  assert.equal(session.term.clearSelectionCalls, 1);
  assert.equal(session.term.selection, '');
});

test('right click after native paste does not read or paste a second time', async () => {
  const clipboardText = '右键按下已粘贴🙂\n下一行';
  const fixture = createFixture({ clipboardText, pasteEmitsData: true });
  const session = openAndActivate(fixture, 'session-1');
  session.term.emitData(clipboardText);
  assert.equal(fixture.writes.length, 1);
  const event = contextEvent();

  session.host.emit('contextmenu', event);
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(event.prevented, 1);
  assert.equal(event.stopped, 1);
  assert.equal(fixture.clipboardReads.length, 0);
  assert.deepEqual(fixture.clipboardWrites, []);
  assert.deepEqual(session.term.pastes, []);
  assert.equal(fixture.writes.length, 1);
});

test('contextmenu copy and keyboard paste failures are contained', async () => {
  const copyFixture = createFixture({
    writeClipboard: async () => { throw new Error('copy denied'); },
  });
  const copySession = openAndActivate(copyFixture, 'copy');
  copySession.term.selection = 'keep selected';
  const copyEvent = contextEvent();

  const pasteFixture = createFixture({
    readClipboard: async () => { throw new Error('paste denied'); },
  });
  const pasteSession = openAndActivate(pasteFixture, 'paste');
  const pasteEvent = {
    type: 'keydown', key: 'v', ctrlKey: true, metaKey: false, shiftKey: false,
    preventDefault() { this.prevented = true; },
  };

  await assert.doesNotReject(async () => {
    copySession.host.emit('contextmenu', copyEvent);
    pasteSession.term.emitKey(pasteEvent);
    await new Promise((resolve) => setImmediate(resolve));
  });

  assert.equal(copyEvent.prevented, 1);
  assert.equal(copyEvent.stopped, 1);
  assert.equal(copySession.term.selection, 'keep selected');
  assert.equal(copySession.term.clearSelectionCalls, 0);
  assert.deepEqual(copyFixture.statuses.at(-1), {
    message: '复制失败: Error: copy denied', kind: 'warn',
  });
  assert.equal(pasteEvent.prevented, true);
  assert.deepEqual(pasteSession.term.pastes, []);
  assert.deepEqual(pasteFixture.statuses.at(-1), {
    message: '粘贴失败: Error: paste denied', kind: 'warn',
  });
});
