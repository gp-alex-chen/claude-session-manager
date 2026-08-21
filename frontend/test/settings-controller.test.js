import test from 'node:test';
import assert from 'node:assert/strict';

import { createAppState } from '../src/state/app-state.js';
import { THEMES } from '../src/themes/catalog.js';
import { createSettingsController } from '../src/settings/controller.js';
import { createUpdateController } from '../src/updates/controller.js';

class FakeClassList {
  constructor() { this.values = new Set(); }
  add(...names) { names.forEach((name) => this.values.add(name)); }
  remove(...names) { names.forEach((name) => this.values.delete(name)); }
  toggle(name, force) {
    const next = force === undefined ? !this.values.has(name) : force;
    if (next) this.values.add(name); else this.values.delete(name);
  }
}

class FakeNode {
  constructor() {
    this.children = [];
    this.dataset = {};
    this.style = { setProperty() {} };
    this.classList = new FakeClassList();
    this.listeners = new Map();
    this._innerHTML = '';
    this.textContent = '';
    this.value = '';
  }
  append(...children) { this.children.push(...children); }
  appendChild(child) { this.children.push(child); return child; }
  addEventListener(name, callback) { this.listeners.set(name, callback); }
  removeEventListener(name) { this.listeners.delete(name); }
  querySelectorAll(selector) {
    const found = [];
    const matches = selector.startsWith('.') ? selector.slice(1) : '';
    const visit = (node) => {
      if (matches && node.className?.split(' ').includes(matches)) found.push(node);
      for (const child of node.children || []) visit(child);
    };
    visit(this);
    return found;
  }
  set innerHTML(value) {
    this._innerHTML = value;
    if (value === '') this.children = [];
  }
  get innerHTML() { return this._innerHTML; }
}

function fixture(options = {}) {
  const state = createAppState();
  const settingsButton = new FakeNode();
  settingsButton.getBoundingClientRect = () => ({ left: 10, top: 700 });
  const settingsMenu = new FakeNode();
  settingsMenu.style.display = 'none';
  const documentRef = {
    documentElement: { dataset: {} },
    addEventListener() {},
    removeEventListener() {},
  };
  const windowRef = { innerHeight: 800, addEventListener() {}, removeEventListener() {} };
  const storageValues = new Map(Object.entries(options.storage || {}));
  const storage = {
    getItem: (key) => storageValues.get(key) || null,
    setItem: (key, value) => storageValues.set(key, value),
  };
  const statuses = [];
  const applied = [];
  const backend = {
    GetShell: options.GetShell || (async () => 'cmd'),
    ShellInstalled: options.ShellInstalled || (async () => true),
    SetShell: options.SetShell || (async () => {}),
    GetVersion: options.GetVersion || (async () => '1.2.3'),
  };
  const updateController = options.updateController || {
    mount: (menu) => {
      const label = new FakeNode();
      label.className = 'settings-group-label';
      label.textContent = '更新';
      menu.appendChild(label);
    },
  };
  const controller = createSettingsController({
    state,
    backend,
    terminalController: { applyTheme: (...args) => applied.push(args) },
    themes: THEMES,
    settingsButton,
    settingsMenu,
    documentRef,
    windowRef,
    storage,
    el: (tag, className, text) => {
      const node = new FakeNode();
      node.className = className || '';
      node.textContent = text || '';
      return node;
    },
    setStatus: (message, kind) => statuses.push({ message, kind }),
    updateController,
  });
  return {
    state,
    controller,
    settingsButton,
    settingsMenu,
    documentRef,
    storage,
    statuses,
    applied,
    backend,
  };
}

function childWith(menu, key, value) {
  return menu.children.find((child) => child.dataset?.[key] === value);
}

test('initialize validates stored UI and terminal themes', async () => {
  const valid = fixture({ storage: { 'ui-theme': 'dark', 'term-theme': 'dracula' } });
  await valid.controller.initialize();
  assert.equal(valid.state.uiTheme, 'dark');
  assert.equal(valid.documentRef.documentElement.dataset.theme, 'dark');
  assert.deepEqual(valid.applied, [['dracula', false]]);
  assert.equal(valid.settingsButton.title, '设置 · v1.2.3');

  const invalid = fixture({ storage: { 'ui-theme': 'sepia', 'term-theme': 'missing' }, GetVersion: async () => {
    throw new Error('offline');
  } });
  await invalid.controller.initialize();
  assert.equal(invalid.state.uiTheme, 'light');
  assert.equal(invalid.state.currentTheme, 'claude');
  assert.deepEqual(invalid.applied, [['claude', false]]);
  assert.equal(invalid.settingsButton.title, '设置');
});

test('shell selection validates pwsh and preserves menu on failure', async () => {
  let setCalls = 0;
  const fixtureData = fixture({
    ShellInstalled: async () => false,
    SetShell: async () => { setCalls += 1; },
  });
  await fixtureData.controller.build();
  fixtureData.settingsMenu.style.display = 'block';
  await childWith(fixtureData.settingsMenu, 'shell', 'pwsh').listeners.get('click')();
  assert.equal(setCalls, 0);
  assert.match(fixtureData.statuses.at(-1).message, /未检测到 pwsh/);

  fixtureData.backend.ShellInstalled = async () => true;
  fixtureData.backend.SetShell = async () => { throw new Error('cannot save'); };
  await childWith(fixtureData.settingsMenu, 'shell', 'pwsh').listeners.get('click')();
  assert.equal(fixtureData.settingsMenu.style.display, 'block');
  assert.match(fixtureData.statuses.at(-1).message, /切换 Shell 失败/);

  const success = fixture({
    ShellInstalled: async () => true,
    SetShell: async () => {},
  });
  await success.controller.build();
  success.settingsMenu.style.display = 'block';
  await childWith(success.settingsMenu, 'shell', 'pwsh').listeners.get('click')();
  assert.equal(success.settingsMenu.style.display, 'none');
  assert.match(success.statuses.at(-1).message, /底层 Shell 已切换: pwsh/);
});

test('first settings click opens CSS-hidden menu and second click closes it', async () => {
  const fixtureData = fixture({ GetShell: async () => { throw new Error('offline'); } });
  fixtureData.settingsMenu.style.display = '';
  fixtureData.controller.start();
  let stopped = false;
  await fixtureData.settingsButton.listeners.get('click')({
    stopPropagation: () => { stopped = true; },
  });
  await Promise.resolve();
  await Promise.resolve();
  assert.equal(stopped, true);
  assert.equal(fixtureData.settingsMenu.style.display, 'block');
  assert.equal(fixtureData.settingsMenu.style.left, '10px');
  assert.ok(fixtureData.settingsMenu.children.length > 0);

  fixtureData.settingsButton.listeners.get('click')({ stopPropagation() {} });
  assert.equal(fixtureData.settingsMenu.style.display, 'none');
});

test('pwsh fallback note is shown when configured shell is unavailable', async () => {
  const fixtureData = fixture({
    GetShell: async () => 'pwsh',
    ShellInstalled: async () => false,
  });
  await fixtureData.controller.build();
  assert.ok(fixtureData.settingsMenu.children.some((child) => child.textContent.includes('cmd 兜底')));
});

test('shell read failure safely selects cmd', async () => {
  const fixtureData = fixture({ GetShell: async () => { throw new Error('offline'); } });
  await fixtureData.controller.build();
  assert.match(childWith(fixtureData.settingsMenu, 'shell', 'cmd').className, /cur/);
});

test('stale shell builds cannot append after a newer menu build', async () => {
  const resolvers = [];
  const fixtureData = fixture({
    GetShell: () => new Promise((resolve) => resolvers.push(resolve)),
  });
  const first = fixtureData.controller.build();
  const second = fixtureData.controller.build();
  await Promise.resolve();
  resolvers[1]('cmd');
  await second;
  resolvers[0]('pwsh');
  await first;
  const labels = fixtureData.settingsMenu.children
    .filter((child) => child.className === 'settings-group-label')
    .map((child) => child.textContent);
  assert.deepEqual(labels, ['界面外观', '终端配色', '底层 Shell', '更新']);
  assert.match(childWith(fixtureData.settingsMenu, 'shell', 'cmd').className, /cur/);
  assert.doesNotMatch(childWith(fixtureData.settingsMenu, 'shell', 'pwsh').className, /cur/);
});
