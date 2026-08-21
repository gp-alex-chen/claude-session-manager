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
  contains(name) { return this.values.has(name); }
  toggle(name, force) {
    const next = force === undefined ? !this.values.has(name) : force;
    if (next) this.values.add(name); else this.values.delete(name);
  }
}

class FakeNode {
  constructor() {
    this.children = [];
    this.dataset = {};
    this.attributes = new Map();
    const styleValues = new Map();
    this.style = {
      setProperty(name, value) { styleValues.set(name, value); },
      getPropertyValue(name) { return styleValues.get(name) || ''; },
    };
    this.classList = new FakeClassList();
    this.listeners = new Map();
    this.listenerAdds = new Map();
    this._innerHTML = '';
    this.textContent = '';
    this.value = '';
    this.disabled = false;
    this.hidden = false;
    this.focused = false;
  }
  append(...children) { children.forEach((child) => this.appendChild(child)); }
  appendChild(child) { child.parentNode = this; this.children.push(child); return child; }
  addEventListener(name, callback) {
    this.listeners.set(name, callback);
    this.listenerAdds.set(name, (this.listenerAdds.get(name) || 0) + 1);
  }
  removeEventListener(name, callback) {
    if (!callback || this.listeners.get(name) === callback) this.listeners.delete(name);
  }
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
  setAttribute(name, value) { this.attributes.set(name, String(value)); }
  getAttribute(name) { return this.attributes.get(name) || null; }
  contains(node) {
    if (node === this) return true;
    return this.children.some((child) => child.contains?.(node));
  }
  focus() { this.focused = true; }
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
    listeners: new Map(),
    addEventListener(name, callback) { this.listeners.set(name, callback); },
    removeEventListener(name) { this.listeners.delete(name); },
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
  const dialogDeps = makeDialogDeps(settingsMenu, documentRef);
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
      node.tagName = tag.toUpperCase();
      node.className = className || '';
      node.textContent = text || '';
      return node;
    },
    setStatus: (message, kind) => statuses.push({ message, kind }),
    updateController,
    ...dialogDeps,
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
    ...dialogDeps,
  };
}

function makeDialogDeps(settingsMenu, documentRef) {
  const settingsDialog = new FakeNode();
  const settingsClose = new FakeNode();
  const settingsNav = new FakeNode();
  const settingsContent = new FakeNode();
  const settingsVersion = new FakeNode();
  const categories = ['appearance', 'terminal', 'update'].map((name) => {
    const button = new FakeNode();
    button.dataset.category = name;
    button.textContent = { appearance: '外观', terminal: '终端', update: '更新' }[name];
    button.setAttribute('role', 'tab');
    button.setAttribute('aria-controls', `settings-panel-${name}`);
    settingsNav.appendChild(button);
    return button;
  });
  const panels = Object.fromEntries(['appearance', 'terminal', 'update'].map((name) => {
    const panel = new FakeNode();
    panel.dataset.panel = name;
    settingsContent.appendChild(panel);
    return [name, panel];
  }));
  settingsMenu.setAttribute('role', 'dialog');
  settingsMenu.setAttribute('aria-modal', 'true');
  settingsNav.setAttribute('role', 'tablist');
  settingsClose.textContent = '关闭';
  settingsDialog.append(settingsClose, settingsVersion, settingsNav, settingsContent);
  settingsMenu.appendChild(settingsDialog);
  settingsMenu.hidden = true;
  return {
    settingsDialog,
    settingsClose,
    settingsNav,
    settingsContent,
    settingsVersion,
    categoryButtons: categories,
    panels,
  };
}

function childWith(menu, key, value) {
  return allNodes(menu).find((child) => child.dataset?.[key] === value);
}

function allNodes(root) {
  const nodes = [];
  const visit = (node) => {
    nodes.push(node);
    for (const child of node.children || []) visit(child);
  };
  visit(root);
  return nodes;
}

function visibleText(node) {
  return (node.textContent || '') + (node.children || []).map((child) => visibleText(child)).join('');
}

function countOccurrences(value, needle) {
  return value.split(needle).length - 1;
}

test('initialize validates stored UI and terminal themes', async () => {
  const valid = fixture({ storage: { 'ui-theme': 'dark', 'term-theme': 'dracula' } });
  await valid.controller.initialize();
  assert.equal(valid.state.uiTheme, 'dark');
  assert.equal(valid.documentRef.documentElement.dataset.theme, 'dark');
  assert.deepEqual(valid.applied, [['dracula', false]]);
  assert.equal(valid.settingsButton.title, '设置 · v1.2.3');

  const tagged = fixture({ GetVersion: async () => 'v0.3-wails-rc2-local' });
  await tagged.controller.initialize();
  assert.equal(tagged.settingsButton.title, '设置 · v0.3-wails-rc2-local');

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
  fixtureData.settingsMenu.hidden = false;
  await childWith(fixtureData.settingsMenu, 'shell', 'pwsh').listeners.get('click')();
  assert.equal(setCalls, 0);
  assert.equal(fixtureData.settingsMenu.hidden, false);
  assert.ok(allNodes(fixtureData.panels.terminal).some((node) => /pwsh.*不可用|不可用.*pwsh/.test(node.textContent)));

  fixtureData.backend.ShellInstalled = async () => true;
  fixtureData.backend.SetShell = async () => { throw new Error('cannot save'); };
  await fixtureData.controller.build();
  await childWith(fixtureData.settingsMenu, 'shell', 'pwsh').listeners.get('click')();
  assert.equal(fixtureData.settingsMenu.hidden, false);
  assert.match(fixtureData.statuses.at(-1).message, /切换 Shell 失败/);

  const success = fixture({
    ShellInstalled: async () => true,
    SetShell: async () => {},
  });
  await success.controller.build();
  success.settingsMenu.hidden = false;
  await childWith(success.settingsMenu, 'shell', 'pwsh').listeners.get('click')();
  assert.equal(success.settingsMenu.hidden, false);
  assert.equal(childWith(success.settingsMenu, 'shell', 'pwsh').getAttribute('aria-pressed'), 'true');
  assert.match(success.statuses.at(-1).message, /底层 Shell 已切换: pwsh/);
});

test('appearance uses semantic mode buttons and theme cards without closing', async () => {
  const fixtureData = fixture();
  fixtureData.settingsMenu.hidden = false;
  await fixtureData.controller.build();

  const modes = ['light', 'dark'].map((mode) => childWith(fixtureData.panels.appearance, 'mode', mode));
  assert.ok(modes.every((button) => button.tagName === 'BUTTON'));
  assert.deepEqual(modes.map((button) => button.getAttribute('aria-pressed')), ['true', 'false']);
  assert.ok(modes.every((button) => button.className.includes('settings-segment')));

  const cards = Object.keys(THEMES).map((theme) => childWith(fixtureData.panels.appearance, 'theme', theme));
  assert.equal(cards.length, 8);
  assert.ok(cards.every((card) => card.tagName === 'BUTTON'));
  assert.ok(cards.every((card) => card.style.getPropertyValue('--theme-bg')));
  assert.ok(cards.every((card) => card.style.getPropertyValue('--theme-fg')));
  for (const [key, theme] of Object.entries(THEMES)) {
    assert.equal(countOccurrences(visibleText(childWith(fixtureData.panels.appearance, 'theme', key)), theme.name), 1);
  }
  assert.equal(cards[0].getAttribute('aria-pressed'), 'true');

  modes[1].listeners.get('click')();
  assert.equal(fixtureData.state.uiTheme, 'dark');
  assert.equal(fixtureData.settingsMenu.hidden, false);
  assert.deepEqual(modes.map((button) => button.getAttribute('aria-pressed')), ['false', 'true']);

  cards[1].listeners.get('click')();
  assert.equal(fixtureData.settingsMenu.hidden, false);
  assert.equal(fixtureData.state.currentTheme, 'dracula');
  assert.equal(cards[1].getAttribute('aria-pressed'), 'true');
  assert.deepEqual(fixtureData.applied.at(-1), ['dracula']);
});

test('pwsh is checked during build and disabled when unavailable', async () => {
  let setCalls = 0;
  let installedChecks = 0;
  const fixtureData = fixture({
    GetShell: async () => 'cmd',
    ShellInstalled: async () => { installedChecks += 1; return false; },
    SetShell: async () => { setCalls += 1; },
  });
  await fixtureData.controller.build();

  assert.equal(installedChecks, 1);
  const pwsh = childWith(fixtureData.panels.terminal, 'shell', 'pwsh');
  assert.equal(pwsh.tagName, 'BUTTON');
  assert.equal(countOccurrences(visibleText(pwsh), 'PowerShell 7'), 1);
  assert.equal(pwsh.disabled, true);
  assert.equal(pwsh.getAttribute('aria-disabled'), 'true');
  await pwsh.listeners.get('click')();
  assert.equal(setCalls, 0);
  assert.ok(allNodes(fixtureData.panels.terminal).some((node) => /pwsh.*不可用|不可用.*pwsh/.test(node.textContent)));
});

test('configured unavailable pwsh stays selected and explains fallback', async () => {
  const fixtureData = fixture({
    GetShell: async () => 'pwsh',
    ShellInstalled: async () => false,
  });
  await fixtureData.controller.build();
  const pwsh = childWith(fixtureData.panels.terminal, 'shell', 'pwsh');
  assert.equal(pwsh.getAttribute('aria-pressed'), 'true');
  assert.equal(pwsh.disabled, true);
  assert.ok(allNodes(fixtureData.panels.terminal).some((node) => node.textContent.includes('cmd 兜底')));
});

test('first settings click opens CSS-hidden menu and second click closes it', async () => {
  const fixtureData = fixture({ GetShell: async () => { throw new Error('offline'); } });
  fixtureData.controller.start();
  let stopped = false;
  await fixtureData.settingsButton.listeners.get('click')({
    stopPropagation: () => { stopped = true; },
  });
  await Promise.resolve();
  await Promise.resolve();
  assert.equal(stopped, true);
  assert.equal(fixtureData.settingsMenu.hidden, false);
  assert.ok(fixtureData.settingsMenu.children.length > 0);

  fixtureData.settingsButton.listeners.get('click')({ stopPropagation() {} });
  assert.equal(fixtureData.settingsMenu.hidden, true);
});

test('pwsh fallback note is shown when configured shell is unavailable', async () => {
  const fixtureData = fixture({
    GetShell: async () => 'pwsh',
    ShellInstalled: async () => false,
  });
  await fixtureData.controller.build();
  assert.ok(allNodes(fixtureData.settingsMenu).some((child) => child.textContent.includes('cmd 兜底')));
});

test('shell read failure safely selects cmd', async () => {
  const fixtureData = fixture({ GetShell: async () => { throw new Error('offline'); } });
  await fixtureData.controller.build();
  assert.equal(childWith(fixtureData.settingsMenu, 'shell', 'cmd').getAttribute('aria-pressed'), 'true');
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
  const labels = allNodes(fixtureData.settingsMenu)
    .filter((child) => child.className === 'settings-section-title' || child.className === 'settings-group-label')
    .map((child) => child.textContent);
  assert.deepEqual(labels, ['界面模式', '终端配色', '底层 Shell', '更新']);
  assert.equal(childWith(fixtureData.settingsMenu, 'shell', 'cmd').getAttribute('aria-pressed'), 'true');
  assert.equal(childWith(fixtureData.settingsMenu, 'shell', 'pwsh').getAttribute('aria-pressed'), 'false');
});

test('settings uses a modal dialog with semantic categories and close paths', async () => {
  const fixtureData = fixture();
  fixtureData.controller.start();
  let buttonStopped = false;
  fixtureData.settingsButton.listeners.get('click')({
    stopPropagation: () => { buttonStopped = true; },
  });
  await Promise.resolve();
  await Promise.resolve();

  assert.equal(buttonStopped, true);
  assert.equal(fixtureData.settingsMenu.hidden, false);
  assert.equal(fixtureData.settingsMenu.getAttribute('role'), 'dialog');
  assert.equal(fixtureData.settingsMenu.getAttribute('aria-hidden'), 'false');
  assert.equal(fixtureData.settingsNav.getAttribute('role'), 'tablist');
  assert.deepEqual(fixtureData.categoryButtons.map((button) => button.textContent), ['外观', '终端', '更新']);
  assert.ok(fixtureData.categoryButtons.every((button) => button.getAttribute('role') === 'tab'));
  assert.deepEqual(
    fixtureData.categoryButtons.map((button) => button.getAttribute('aria-controls')),
    ['settings-panel-appearance', 'settings-panel-terminal', 'settings-panel-update'],
  );
  assert.deepEqual(
    fixtureData.categoryButtons.map((button) => button.getAttribute('aria-selected')),
    ['true', 'false', 'false'],
  );
  assert.equal(typeof fixtureData.settingsClose.listeners.get('click'), 'function');

  let dialogStopped = false;
  fixtureData.settingsDialog.listeners.get('click')({
    stopPropagation: () => { dialogStopped = true; },
  });
  assert.equal(dialogStopped, true);

  fixtureData.settingsMenu.listeners.get('click')({ target: fixtureData.settingsMenu });
  assert.equal(fixtureData.settingsMenu.hidden, true);
  assert.equal(fixtureData.settingsButton.focused, true);

  fixtureData.settingsButton.focused = false;
  fixtureData.settingsButton.listeners.get('click')({ stopPropagation() {} });
  fixtureData.documentRef.listeners.get('keydown')({ key: 'Escape' });
  assert.equal(fixtureData.settingsMenu.hidden, true);
  assert.equal(fixtureData.settingsButton.focused, true);
});

test('settings category persists across close and stale shell builds cannot write after close', async () => {
  const resolvers = [];
  const fixtureData = fixture({
    GetShell: () => new Promise((resolve) => resolvers.push(resolve)),
  });
  fixtureData.controller.start();
  fixtureData.settingsButton.listeners.get('click')({ stopPropagation() {} });
  await Promise.resolve();
  fixtureData.categoryButtons[1].listeners.get('click')({ stopPropagation() {} });
  assert.equal(fixtureData.panels.terminal.hidden, false);
  assert.equal(fixtureData.panels.appearance.hidden, true);
  fixtureData.settingsClose.listeners.get('click')({ stopPropagation() {} });
  assert.equal(fixtureData.settingsMenu.hidden, true);

  resolvers[0]('pwsh');
  await Promise.resolve();
  await Promise.resolve();
  assert.equal(fixtureData.panels.terminal.children.length, 0);

  fixtureData.settingsButton.listeners.get('click')({ stopPropagation() {} });
  assert.equal(fixtureData.panels.terminal.hidden, false);
  assert.equal(fixtureData.panels.appearance.hidden, true);
});

test('settings start and stop do not accumulate listeners', () => {
  const fixtureData = fixture();
  fixtureData.controller.start();
  fixtureData.controller.start();
  assert.equal(fixtureData.settingsButton.listenerAdds.get('click'), 1);
  assert.equal(fixtureData.categoryButtons[0].listenerAdds.get('click'), 1);

  fixtureData.controller.stop();
  fixtureData.controller.stop();
  assert.equal(fixtureData.settingsButton.listeners.size, 0);
  assert.equal(fixtureData.categoryButtons[0].listeners.size, 0);

  fixtureData.controller.start();
  assert.equal(fixtureData.settingsButton.listenerAdds.get('click'), 2);
  assert.equal(fixtureData.categoryButtons[0].listenerAdds.get('click'), 2);
  assert.equal(fixtureData.categoryButtons[0].listeners.size, 1);
  fixtureData.controller.stop();
});
