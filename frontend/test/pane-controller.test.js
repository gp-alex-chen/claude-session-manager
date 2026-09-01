import test from 'node:test';
import assert from 'node:assert/strict';

import { createPaneController } from '../src/panes/controller.js';
import { createAppState } from '../src/state/app-state.js';

class FakeClassList {
  constructor() { this.values = new Set(); }
  add(...names) { names.forEach((name) => this.values.add(name)); }
  remove(...names) { names.forEach((name) => this.values.delete(name)); }
  contains(name) { return this.values.has(name); }
  toggle(name, force) {
    const next = force === undefined ? !this.values.has(name) : force;
    if (next) this.values.add(name);
    else this.values.delete(name);
    return next;
  }
  forEach(callback) { this.values.forEach(callback); }
}

class FakeNode {
  constructor(tag = 'div') {
    this.tagName = tag.toUpperCase();
    this.children = [];
    this.parentNode = null;
    this.dataset = {};
    this.classList = new FakeClassList();
    this.listeners = new Map();
    this.attributes = new Map();
    this.style = {
      values: new Map(),
      setProperty: (name, value) => this.style.values.set(name, String(value)),
      getPropertyValue: (name) => this.style.values.get(name) || '',
    };
    this.hidden = false;
    this.disabled = false;
    this.value = '';
    this.textContent = '';
    this.getBoundingClientRect = () => ({ left: 0, top: 0, width: 100, height: 100 });
  }
  append(...children) { children.forEach((child) => this.appendChild(child)); }
  appendChild(child) {
    if (child.parentNode) child.parentNode.children = child.parentNode.children.filter((item) => item !== child);
    child.parentNode = this;
    this.children.push(child);
    return child;
  }
  replaceChildren(...children) {
    this.children = [];
    children.forEach((child) => this.appendChild(child));
  }
  addEventListener(name, callback) { this.listeners.set(name, callback); }
  removeEventListener(name, callback) {
    if (this.listeners.get(name) === callback) this.listeners.delete(name);
  }
  dispatchEvent(event) { return this.listeners.get(event.type)?.(event); }
  click() {
    return this.dispatchEvent({ type: 'click', target: this, stopPropagation() {} });
  }
  remove() {
    if (this.parentNode) this.parentNode.children = this.parentNode.children.filter((item) => item !== this);
    this.parentNode = null;
  }
  contains(target) {
    return target === this || this.children.some((child) => child.contains?.(target));
  }
  setAttribute(name, value) { this.attributes.set(name, String(value)); }
  getAttribute(name) { return this.attributes.get(name); }
}

class FakeResizeObserver {
  static instances = [];
  constructor(callback) {
    this.callback = callback;
    this.targets = [];
    FakeResizeObserver.instances.push(this);
  }
  observe(target) { this.targets.push(target); }
  disconnect() { this.targets = []; }
  trigger(...paneIds) {
    this.callback(paneIds.map((paneId) => ({ target: paneId })));
  }
}

function makeFixture(options = {}) {
  const state = createAppState();
  const terminalRoot = new FakeNode('main');
  const statusBar = new FakeNode('header');
  const documentRef = new FakeNode('document');
  documentRef.createElement = (tag) => new FakeNode(tag);
  const frames = [];
  const storageValues = new Map();
  if (options.layoutMode) storageValues.set('terminal-layout-mode', options.layoutMode);
  if (options.splitRatios) {
    storageValues.set('terminal-layout-split-ratios', JSON.stringify(options.splitRatios));
  }
  const mounts = [];
  const unmounts = [];
  const focuses = [];
  const resizes = [];
  const statuses = [];
  const changes = [];
  const terminalController = {
    mountSession(token, body) { mounts.push([token, body]); },
    unmountSession(token, pool) { unmounts.push([token, pool]); },
    focusSession(token, options) { focuses.push([token, options]); },
    resizeVisible(tokens) { resizes.push(tokens); },
  };
  const controller = createPaneController({
    state,
    terminalController,
    terminalRoot,
    statusBar,
    documentRef,
    storageRef: {
      getItem: (key) => storageValues.get(key) || null,
      setItem: (key, value) => storageValues.set(key, value),
    },
    el: (tag, className, text) => {
      const node = new FakeNode(tag);
      node.className = className || '';
      node.textContent = text || '';
      return node;
    },
    setStatus: (message, kind) => statuses.push({ message, kind }),
    onFocus: (token) => focuses.push([token, { empty: true }]),
    onChange: (nextState) => changes.push(nextState),
    requestFrame: (callback) => { frames.push(callback); return frames.length; },
    ResizeObserverCtor: FakeResizeObserver,
  });
  for (const token of ['a', 'b', 'c', 'd']) {
    state.terminals.set(token, { token, labelText: 'Session ' + token, exited: false, visible: false, host: new FakeNode() });
  }
  return {
    state,
    controller,
    terminalRoot,
    documentRef,
    mounts,
    unmounts,
    focuses,
    resizes,
    statuses,
    changes,
    storageValues,
    frames,
    flushFrame: () => frames.shift()?.(),
    observer: () => FakeResizeObserver.instances.at(-1),
  };
}

test('fixed layouts expose the requested visible pane geometry', () => {
  const fixture = makeFixture();
  fixture.controller.initialize();

  for (const mode of ['split-rows-2', 'split-cols-2', 'split-main-left-3', 'grid-2x2']) {
    fixture.controller.setLayout(mode);
    const visible = fixture.state.panes.filter((pane) => !fixture.controller.view.paneRoot(pane.id).hidden);
    const expected = mode === 'split-main-left-3' ? 3 : (mode === 'grid-2x2' ? 4 : 2);
    assert.equal(visible.length, expected, mode);
    assert.equal(fixture.terminalRoot.dataset.layoutMode, mode);
    assert.equal(fixture.terminalRoot.dataset.focusedPaneId, 'pane-0', mode);
  }
});

test('split layouts expose only the divider orientation they need', () => {
  const fixture = makeFixture();
  const vertical = fixture.controller.view.divider('vertical');
  const horizontal = fixture.controller.view.divider('horizontal');

  assert.equal(vertical.hidden, true);
  assert.equal(horizontal.hidden, true);

  fixture.controller.setLayout('split-cols-2');
  assert.equal(vertical.hidden, false);
  assert.equal(horizontal.hidden, true);
  assert.equal(vertical.getAttribute('role'), 'separator');
  assert.equal(vertical.getAttribute('aria-orientation'), 'vertical');
  assert.equal(vertical.getAttribute('aria-label'), '调整左右窗格宽度');

  fixture.controller.setLayout('split-rows-2');
  assert.equal(vertical.hidden, true);
  assert.equal(horizontal.hidden, false);
  assert.equal(horizontal.getAttribute('aria-orientation'), 'horizontal');
  assert.equal(horizontal.getAttribute('aria-label'), '调整上下窗格高度');

  fixture.controller.setLayout('grid-2x2');
  assert.equal(vertical.hidden, false);
  assert.equal(horizontal.hidden, false);
});

test('pointer dragging adjusts a vertical divider, persists it, and resizes visible terminals', () => {
  const fixture = makeFixture();
  fixture.controller.setLayout('split-cols-2');
  fixture.flushFrame();
  const divider = fixture.controller.view.divider('vertical');
  let prevented = false;

  divider.dispatchEvent({
    type: 'pointerdown',
    button: 0,
    pointerId: 7,
    clientX: 50,
    clientY: 20,
    currentTarget: divider,
    preventDefault: () => { prevented = true; },
  });
  assert.equal(prevented, true);
  assert.equal(fixture.terminalRoot.classList.contains('is-resizing'), true);

  fixture.documentRef.dispatchEvent({
    type: 'pointermove',
    pointerId: 7,
    clientX: 70,
    clientY: 20,
    preventDefault() {},
  });
  const ratio = Number.parseFloat(
    fixture.terminalRoot.style.getPropertyValue('--terminal-split-columns-first'),
  );
  assert.ok(ratio > 0.7 && ratio < 0.73, ratio);
  assert.equal(fixture.frames.length, 1);

  fixture.flushFrame();
  assert.deepEqual(fixture.resizes.at(-1), ['a', 'b']);
  fixture.documentRef.dispatchEvent({ type: 'pointerup', pointerId: 7 });
  assert.equal(fixture.terminalRoot.classList.contains('is-resizing'), false);

  const persisted = JSON.parse(fixture.storageValues.get('terminal-layout-split-ratios'));
  assert.deepEqual(persisted['split-cols-2'].vertical, { first: ratio });
});

test('horizontal divider keyboard controls clamp and resize its row split', () => {
  const fixture = makeFixture();
  fixture.controller.setLayout('split-rows-2');
  const divider = fixture.controller.view.divider('horizontal');
  let prevented = 0;

  divider.dispatchEvent({ type: 'keydown', key: 'ArrowDown', shiftKey: false, preventDefault: () => { prevented += 1; } });
  assert.equal(divider.getAttribute('aria-valuenow'), '52');
  divider.dispatchEvent({ type: 'keydown', key: 'Home', preventDefault: () => { prevented += 1; } });
  assert.equal(divider.getAttribute('aria-valuenow'), '16');
  divider.dispatchEvent({ type: 'keydown', key: 'End', preventDefault: () => { prevented += 1; } });
  assert.equal(divider.getAttribute('aria-valuenow'), '84');
  divider.dispatchEvent({ type: 'keydown', key: 'ArrowLeft', preventDefault: () => { prevented += 1; } });
  assert.equal(divider.getAttribute('aria-valuenow'), '84');
  assert.equal(prevented, 3);
});

test('saved split ratios restore independently for each layout', () => {
  const fixture = makeFixture({
    splitRatios: {
      'split-cols-2': { vertical: { first: 0.72 } },
      'split-rows-2': { horizontal: { first: 0.28 } },
    },
  });

  fixture.controller.setLayout('split-cols-2');
  assert.equal(fixture.controller.view.divider('vertical').getAttribute('aria-valuenow'), '72');
  assert.equal(
    fixture.terminalRoot.style.getPropertyValue('--terminal-split-columns-first'),
    '0.72fr',
  );

  fixture.controller.setLayout('split-rows-2');
  assert.equal(fixture.controller.view.divider('horizontal').getAttribute('aria-valuenow'), '28');
  assert.equal(
    fixture.terminalRoot.style.getPropertyValue('--terminal-split-rows-first'),
    '0.28fr',
  );
});

test('pane headers use current, session, usage, and clear slots without duplicate titles', () => {
  const fixture = makeFixture();
  const firstRoot = fixture.controller.view.paneRoot('pane-0');
  const firstHeader = firstRoot.children[0];

  assert.deepEqual(firstHeader.children.map((child) => child.className), [
    'terminal-pane-current',
    'terminal-pane-session',
    'terminal-pane-usage-summary',
    'terminal-pane-clear',
  ]);
  assert.equal(firstRoot.children[1].className, 'terminal-pane-usage-details');
  assert.equal(firstRoot.children[2].className, 'terminal-pane-body');
  assert.equal(firstHeader.children[0].textContent, '当前');
  assert.equal(firstHeader.children[0].getAttribute('aria-hidden'), 'false');
  assert.equal(fixture.controller.view.paneRoot('pane-1').children[0].children[0].getAttribute('aria-hidden'), 'true');
  assert.equal(firstHeader.children.some((child) => child.className === 'terminal-pane-title'), false);
  assert.equal(firstHeader.children[2].disabled, true);
});

test('focused pane marker moves without removing its fixed slot', () => {
  const fixture = makeFixture();
  fixture.controller.setLayout('split-cols-2');
  fixture.controller.view.paneBody('pane-1').click();

  assert.equal(fixture.controller.view.paneRoot('pane-0').children[0].children[0].getAttribute('aria-hidden'), 'true');
  assert.equal(fixture.controller.view.paneRoot('pane-1').children[0].children[0].getAttribute('aria-hidden'), 'false');
  assert.equal(fixture.controller.view.paneRoot('pane-0').children[0].children[0].hidden, false);
  assert.equal(fixture.controller.view.paneRoot('pane-1').children[0].children[0].hidden, false);
});

test('pane mutations notify the usage surface after DOM state is synchronized', () => {
  const fixture = makeFixture();
  fixture.controller.initialize();

  assert.ok(fixture.changes.length >= 1);
  const initialChanges = fixture.changes.length;
  fixture.controller.setLayout('split-cols-2');

  assert.ok(fixture.changes.length > initialChanges);
  assert.equal(fixture.changes.at(-1), fixture.state);
});

test('layout controller restores a valid saved mode and normalizes invalid storage', async () => {
  const saved = makeFixture({ layoutMode: 'grid-2x2' });
  await saved.controller.initialize();
  assert.equal(saved.state.layoutMode, 'grid-2x2');
  assert.equal(saved.controller.getVisiblePaneIds().length, 4);

  const invalid = makeFixture({ layoutMode: 'manual-drag' });
  await invalid.controller.initialize();
  assert.equal(invalid.state.layoutMode, 'single');
  assert.equal(invalid.controller.setLayout('split-rows-2'), 'split-rows-2');
  assert.equal(invalid.storageValues.get('terminal-layout-mode'), 'split-rows-2');
});

test('changing layout keeps the selected mode across stop and start', async () => {
  const fixture = makeFixture();
  fixture.controller.initialize();
  fixture.controller.setLayout('grid-2x2');
  fixture.controller.stop();
  fixture.controller.start();
  await fixture.controller.initialize();

  assert.equal(fixture.state.layoutMode, 'grid-2x2');
  assert.equal(fixture.controller.getVisiblePaneIds().length, 4);
});

test('layout changes retain sessions in stable order and clear panes removed by the preset', () => {
  const fixture = makeFixture();
  fixture.controller.initialize();
  fixture.controller.setLayout('grid-2x2');

  assert.deepEqual(fixture.state.panes.map((pane) => pane.token), ['a', 'b', 'c', 'd']);
  fixture.controller.setLayout('split-main-left-3');
  assert.deepEqual(fixture.state.panes.map((pane) => pane.token), ['a', 'b', 'c', null]);
  assert.deepEqual(fixture.unmounts.map(([token]) => token), ['d']);
  fixture.controller.setLayout('split-cols-2');
  assert.deepEqual(fixture.state.panes.map((pane) => pane.token), ['a', 'b', null, null]);
  assert.deepEqual(fixture.unmounts.map(([token]) => token), ['d', 'c']);
  assert.equal(fixture.state.layoutMode, 'split-cols-2');
});

test('layout switching batches one resize after pane geometry settles', () => {
  const fixture = makeFixture();
  fixture.controller.initialize();

  fixture.controller.setLayout('split-cols-2');

  assert.equal(fixture.frames.length, 1);
  assert.deepEqual(fixture.resizes, []);
  fixture.flushFrame();
  assert.deepEqual(fixture.resizes, [['a', 'b']]);
});

test('showing an existing session focuses its original pane and never duplicates it', () => {
  const fixture = makeFixture();
  fixture.controller.initialize();
  fixture.controller.setLayout('split-cols-2');
  fixture.controller.showSession('a', { paneId: 'pane-0', focus: false });
  fixture.controller.showSession('a', { paneId: 'pane-1' });

  assert.deepEqual(fixture.state.panes.map((pane) => pane.token), ['a', 'b', null, null]);
  assert.equal(fixture.state.panes.filter((pane) => pane.token === 'a').length, 1);
  assert.equal(fixture.state.focusedPaneId, 'pane-0');
  assert.deepEqual(fixture.focuses.at(-1), ['a', { focus: true }]);
});

test('clearing a pane unmounts its session without killing it and focuses a remaining pane', () => {
  const fixture = makeFixture();
  fixture.controller.initialize();
  fixture.controller.setLayout('split-cols-2');
  fixture.controller.showSession('a', { paneId: 'pane-0', focus: false });
  fixture.controller.showSession('b', { paneId: 'pane-1' });
  fixture.controller.clearPane('pane-1');

  assert.equal(fixture.state.panes[1].token, null);
  assert.deepEqual(fixture.unmounts.map(([token]) => token), ['b']);
  assert.deepEqual(fixture.focuses.at(-1), ['a', { focus: true }]);
});

test('assigning a session to an occupied pane unmounts the displaced session', () => {
  const fixture = makeFixture();
  fixture.controller.initialize();
  fixture.controller.setLayout('single');
  fixture.controller.showSession('b', { paneId: 'pane-0', focus: false });

  assert.deepEqual(fixture.state.panes.map((pane) => pane.token), ['b', null, null, null]);
  assert.equal(fixture.unmounts.at(-1)[0], 'a');
});

test('resize observer batches the tokens assigned to visible panes', () => {
  const fixture = makeFixture();
  fixture.controller.initialize();
  fixture.controller.setLayout('split-cols-2');
  fixture.flushFrame();
  fixture.controller.start();
  const pane0 = fixture.controller.view.paneBody('pane-0');
  const pane1 = fixture.controller.view.paneBody('pane-1');
  fixture.observer().callback([{ target: pane0 }, { target: pane1 }]);
  assert.equal(fixture.frames.length, 1);
  fixture.flushFrame();
  assert.deepEqual(fixture.resizes.at(-1).sort(), ['a', 'b']);
  fixture.controller.stop();
});

test('resize observer batches only visible pane tokens', () => {
  const fixture = makeFixture();
  fixture.controller.initialize();
  fixture.controller.setLayout('split-cols-2');
  fixture.flushFrame();
  fixture.controller.showSession('a', { paneId: 'pane-0', focus: false });
  fixture.controller.showSession('b', { paneId: 'pane-1', focus: false });
  const pane0Body = fixture.controller.view.paneBody('pane-0');
  const pane1Body = fixture.controller.view.paneBody('pane-1');
  fixture.observer().callback([{ target: pane0Body }, { target: pane1Body }]);
  assert.equal(fixture.frames.length, 1);
  fixture.flushFrame();
  assert.deepEqual(fixture.resizes.at(-1).sort(), ['a', 'b']);
});

test('clicking a pane updates focus without hiding the other visible pane', () => {
  const fixture = makeFixture();
  fixture.controller.initialize();
  fixture.controller.setLayout('split-cols-2');
  fixture.controller.showSession('a', { paneId: 'pane-0', focus: false });
  fixture.controller.showSession('b', { paneId: 'pane-1', focus: false });
  fixture.controller.view.paneBody('pane-1').click();

  assert.equal(fixture.state.focusedPaneId, 'pane-1');
  assert.equal(fixture.state.activeToken, 'b');
  assert.equal(fixture.terminalRoot.dataset.focusedPaneId, 'pane-1');
  assert.equal(fixture.controller.view.paneRoot('pane-0').hidden, false);
  assert.equal(fixture.controller.view.paneRoot('pane-1').hidden, false);
});

test('clicking a pane selector updates the pane without stealing native select focus', () => {
  const fixture = makeFixture();
  fixture.controller.initialize();
  fixture.controller.setLayout('split-cols-2');
  fixture.controller.view.paneSelector('pane-0').click();

  assert.equal(fixture.state.focusedPaneId, 'pane-0');
  assert.deepEqual(fixture.focuses.at(-1), ['a', { focus: false }]);
});

test('an exited assigned session keeps its pane label and disabled selector option', () => {
  const fixture = makeFixture();
  fixture.controller.initialize();
  fixture.controller.setLayout('single');
  fixture.controller.setSessionOptions([{ token: 'a', label: 'Cached A' }]);
  fixture.state.terminals.get('a').exited = true;
  fixture.controller.handleTerminalExit('a');

  const selector = fixture.controller.view.paneSelector('pane-0');
  const selected = selector.children.find((option) => option.value === 'a');
  assert.equal(selector.value, 'a');
  assert.equal(selected.disabled, true);
  assert.equal(selected.textContent, 'Session a（已退出）');
});
