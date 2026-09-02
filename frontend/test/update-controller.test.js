import test from 'node:test';
import assert from 'node:assert/strict';

import { clampProgress } from '../src/utils.js';
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
    this.attributes = new Map();
    this.classList = new FakeClassList();
    this.listeners = new Map();
    this.style = { setProperty(name, value) { this[name] = value; } };
    this.textContent = '';
    this.className = '';
    this.hidden = false;
    this.disabled = false;
  }
  append(...children) {
    children.forEach((child) => { child.parentNode = this; this.children.push(child); });
  }
  appendChild(child) { child.parentNode = this; this.children.push(child); return child; }
  addEventListener(name, callback) { this.listeners.set(name, callback); }
  removeEventListener(name, callback) {
    if (!callback || this.listeners.get(name) === callback) this.listeners.delete(name);
  }
  setAttribute(name, value) { this.attributes.set(name, String(value)); }
  getAttribute(name) { return this.attributes.get(name) || null; }
  querySelectorAll(selector) {
    const found = [];
    const wanted = selector.slice(1);
    const visit = (node) => {
      if (node.className?.split(' ').includes(wanted)) found.push(node);
      for (const child of node.children) visit(child);
    };
    visit(this);
    return found;
  }
  remove() {
    this.removed = true;
    if (this.parentNode) this.parentNode.children = this.parentNode.children.filter((child) => child !== this);
  }
}

function fixture(options = {}) {
  const calls = { check: 0, apply: 0 };
  const statuses = [];
  const toasts = [];
  const menu = new FakeNode();
  const backend = {
    CheckForUpdate: options.check || (async () => ({ hasUpdate: false, current: '1.0.0' })),
    UpdateToLatest: options.apply || (async () => {}),
  };
  const controller = createUpdateController({
    backend: {
      CheckForUpdate: async (...args) => {
        calls.check += 1;
        return backend.CheckForUpdate(...args);
      },
      UpdateToLatest: async (...args) => {
        calls.apply += 1;
        return backend.UpdateToLatest(...args);
      },
    },
    el: (tag, className, text) => {
      const node = new FakeNode();
      node.tagName = tag.toUpperCase();
      node.className = className || '';
      node.textContent = text ?? '';
      return node;
    },
    setStatus: (message, kind) => statuses.push({ message, kind }),
    showToast: (message) => toasts.push(message),
    clampProgress,
    noticeNode: options.noticeNode,
    storage: options.storage,
    nowFn: options.nowFn,
    setTimeoutFn: options.setTimeoutFn,
    clearTimeoutFn: options.clearTimeoutFn,
  });
  controller.mount(menu);
  const actionButton = findClass(menu, 'update-action');
  return {
    controller,
    menu,
    item: actionButton,
    actionButton,
    versionNode: findClass(menu, 'update-current-version'),
    statusNode: findClass(menu, 'update-status'),
    progressRegion: findClass(menu, 'update-progress-region'),
    progressBar: findClass(menu, 'update-progress-bar'),
    warningNode: findClass(menu, 'update-warning'),
    noticeNode: options.noticeNode,
    statuses,
    toasts,
    calls,
    backend,
  };
}

function findClass(root, className) {
  if (root.className?.split(' ').includes(className)) return root;
  for (const child of root.children || []) {
    const found = findClass(child, className);
    if (found) return found;
  }
  return null;
}

test('check no-update and reject states return to retryable idle', async () => {
  const noUpdate = fixture({ check: async () => ({ hasUpdate: false, current: 'v0.3-wails-rc2-local' }) });
  await noUpdate.item.listeners.get('click')();
  assert.equal(noUpdate.controller.getSnapshot().mode, 'idle');
  assert.equal(noUpdate.calls.check, 1);
  assert.equal(noUpdate.item.disabled, false);
  assert.match(noUpdate.statusNode.textContent, /当前已是最新版本/);
  assert.equal(noUpdate.statuses.at(-1).message, '✅ 已是最新版本（v0.3-wails-rc2-local）');

  const noPrefix = fixture({ check: async () => ({ hasUpdate: false, current: '0.3-wails-rc2-local' }) });
  await noPrefix.item.listeners.get('click')();
  assert.equal(noPrefix.statuses.at(-1).message, '✅ 已是最新版本（v0.3-wails-rc2-local）');

  const rejected = fixture({ check: async () => { throw new Error('offline'); } });
  await rejected.item.listeners.get('click')();
  assert.equal(rejected.controller.getSnapshot().mode, 'idle');
  assert.equal(rejected.controller.getSnapshot().busy, false);
  assert.match(rejected.statuses.at(-1).message, /offline/);
});

test('daily check runs once per local date and shows a quiet new-version notice', async () => {
  const storageValues = new Map();
  const noticeNode = new FakeNode();
  noticeNode.hidden = true;
  let currentDate = new Date('2026-09-02T10:00:00');
  const fixtureData = fixture({
    noticeNode,
    storage: {
      getItem: (key) => storageValues.get(key) || null,
      setItem: (key, value) => storageValues.set(key, value),
    },
    nowFn: () => currentDate,
    check: async () => ({ hasUpdate: true, latest: '2.0.0', current: '1.0.0' }),
  });

  await fixtureData.controller.checkDaily();
  await fixtureData.controller.checkDaily();
  assert.equal(fixtureData.calls.check, 1);
  assert.equal(noticeNode.hidden, false);
  assert.equal(noticeNode.textContent, '有新版本');
  assert.equal(fixtureData.statuses.length, 0);

  currentDate = new Date('2026-09-03T10:00:00');
  await fixtureData.controller.checkDaily();
  assert.equal(fixtureData.calls.check, 2);
  assert.equal(storageValues.size, 1);
  assert.deepEqual([...storageValues.values()], ['2026-09-03']);
});

test('daily check lifecycle schedules at local midnight and clears its timer', async () => {
  const storageValues = new Map();
  const timers = [];
  const cleared = [];
  const fixtureData = fixture({
    storage: {
      getItem: (key) => storageValues.get(key) || null,
      setItem: (key, value) => storageValues.set(key, value),
    },
    nowFn: () => new Date('2026-09-02T10:00:00'),
    setTimeoutFn: (callback, delay) => {
      timers.push({ callback, delay });
      return timers.length;
    },
    clearTimeoutFn: (id) => cleared.push(id),
  });

  fixtureData.controller.start();
  fixtureData.controller.start();
  assert.equal(fixtureData.calls.check, 1);
  assert.equal(timers.length, 1);
  assert.ok(timers[0].delay > 0);
  fixtureData.controller.stop();
  assert.deepEqual(cleared, [1]);
});

test('midnight check catches up when the previous date request is still pending', async () => {
  const storageValues = new Map();
  const timers = [];
  const cleared = [];
  let currentDate = new Date('2026-09-02T23:59:00');
  let resolveFirst;
  const noticeNode = new FakeNode();
  noticeNode.hidden = true;
  const fixtureData = fixture({
    noticeNode,
    storage: {
      getItem: (key) => storageValues.get(key) || null,
      setItem: (key, value) => storageValues.set(key, value),
    },
    nowFn: () => currentDate,
    setTimeoutFn: (callback, delay) => {
      timers.push({ callback, delay });
      return timers.length;
    },
    clearTimeoutFn: (id) => cleared.push(id),
    check: () => {
      if (!resolveFirst) {
        return new Promise((resolve) => {
          resolveFirst = () => resolve({ hasUpdate: false, current: '1.0.0' });
        });
      }
      return Promise.resolve({ hasUpdate: true, latest: '2.0.0', current: '1.0.0' });
    },
  });

  fixtureData.controller.start();
  assert.equal(fixtureData.calls.check, 1);
  currentDate = new Date('2026-09-03T00:00:01');
  const midnight = timers[0].callback();
  resolveFirst();
  await midnight;

  assert.equal(fixtureData.calls.check, 2);
  assert.deepEqual([...storageValues.values()], ['2026-09-03']);
  assert.equal(noticeNode.hidden, false);
  fixtureData.controller.stop();
  assert.deepEqual(cleared, [2]);
});

test('silent daily failures preserve an existing update notice', async () => {
  const storageValues = new Map();
  let currentDate = new Date('2026-09-02T10:00:00');
  let checks = 0;
  const noticeNode = new FakeNode();
  noticeNode.hidden = true;
  const fixtureData = fixture({
    noticeNode,
    storage: {
      getItem: (key) => storageValues.get(key) || null,
      setItem: (key, value) => storageValues.set(key, value),
    },
    nowFn: () => currentDate,
    check: async () => {
      checks += 1;
      if (checks === 1) return { hasUpdate: true, latest: '2.0.0', current: '1.0.0' };
      throw new Error('offline');
    },
  });

  await fixtureData.controller.checkDaily();
  currentDate = new Date('2026-09-03T10:00:00');
  await fixtureData.controller.checkDaily();

  assert.equal(noticeNode.hidden, false);
  assert.equal(noticeNode.textContent, '有新版本');
  assert.equal(fixtureData.controller.getSnapshot().mode, 'ready');
  assert.equal(fixtureData.controller.getSnapshot().info.latest, '2.0.0');
  assert.deepEqual([...storageValues.values()], ['2026-09-02']);
  assert.equal(fixtureData.statuses.length, 0);
});

test('stopping invalidates an in-flight daily check result', async () => {
  const storageValues = new Map();
  const timers = [];
  let resolveCheck;
  const noticeNode = new FakeNode();
  noticeNode.hidden = true;
  const fixtureData = fixture({
    noticeNode,
    storage: {
      getItem: (key) => storageValues.get(key) || null,
      setItem: (key, value) => storageValues.set(key, value),
    },
    setTimeoutFn: (callback) => {
      timers.push(callback);
      return timers.length;
    },
    clearTimeoutFn: () => {},
    check: () => new Promise((resolve) => { resolveCheck = resolve; }),
  });

  fixtureData.controller.start();
  fixtureData.controller.stop();
  resolveCheck({ hasUpdate: true, latest: '2.0.0', current: '1.0.0' });
  await Promise.resolve();

  assert.equal(noticeNode.hidden, true);
  assert.equal(fixtureData.controller.getSnapshot().mode, 'idle');
  assert.equal(storageValues.size, 0);
  assert.equal(timers.length, 1);
});

test('restarting after stop starts a fresh daily check generation', async () => {
  const storageValues = new Map();
  const timers = [];
  const resolvers = [];
  const fixtureData = fixture({
    storage: {
      getItem: (key) => storageValues.get(key) || null,
      setItem: (key, value) => storageValues.set(key, value),
    },
    nowFn: () => new Date('2026-09-02T10:00:00'),
    setTimeoutFn: (callback) => {
      timers.push(callback);
      return timers.length;
    },
    clearTimeoutFn: () => {},
    check: () => new Promise((resolve) => { resolvers.push(resolve); }),
  });

  fixtureData.controller.start();
  fixtureData.controller.stop();
  fixtureData.controller.start();
  assert.equal(fixtureData.calls.check, 2);

  resolvers.forEach((resolve) => resolve({ hasUpdate: false, current: '1.0.0' }));
  await Promise.resolve();
  await Promise.resolve();
  fixtureData.controller.stop();
  assert.equal(timers.length, 2);
});

test('available update leaves checking mode and action invokes UpdateToLatest', async () => {
  const fixtureData = fixture({
    check: async () => ({ hasUpdate: true, latest: '2.0.0', current: '1.0.0' }),
  });
  await fixtureData.item.listeners.get('click')();
  assert.equal(fixtureData.controller.getSnapshot().mode, 'ready');
  assert.equal(fixtureData.controller.getSnapshot().busy, false);
  await fixtureData.item.listeners.get('click')();
  assert.equal(fixtureData.calls.apply, 1);
  assert.equal(fixtureData.controller.getSnapshot().mode, 'idle');
});

test('ready information survives remount and remains actionable', async () => {
  let applied = 0;
  const fixtureData = fixture({
    check: async () => ({ hasUpdate: true, latest: '2.0.0', current: '1.0.0' }),
    apply: async () => { applied += 1; },
  });
  await fixtureData.controller.check();
  const replacement = new FakeNode();
  fixtureData.controller.mount(replacement);
  const replacementAction = findClass(replacement, 'update-action');
  assert.match(findClass(replacement, 'update-status').textContent, /发现新版本 v2.0.0/);
  assert.equal(findClass(replacement, 'update-warning').hidden, false);
  await replacementAction.listeners.get('click')();
  assert.equal(applied, 1);
});

test('update card has semantic controls and a single formatted current version', () => {
  const fixtureData = fixture();
  fixtureData.controller.setCurrentVersion('0.3-wails-rc2-local');
  assert.equal(fixtureData.versionNode.textContent, 'v0.3-wails-rc2-local');
  assert.equal(fixtureData.actionButton.tagName, 'BUTTON');
  assert.equal(fixtureData.actionButton.type, 'button');
  assert.equal(fixtureData.statusNode.getAttribute('aria-live'), 'polite');
  assert.equal(fixtureData.progressBar.getAttribute('role'), 'progressbar');
  assert.equal(fixtureData.warningNode.hidden, true);
});

test('progress is clamped and download state renders a percentage', () => {
  let resolveApply;
  const fixtureData = fixture({
    check: async () => ({ hasUpdate: true, latest: '2.0.0' }),
    apply: () => new Promise((resolve) => { resolveApply = resolve; }),
  });
  return fixtureData.controller.check().then(async () => {
    const applying = fixtureData.item.listeners.get('click')();
    fixtureData.controller.handleState('下载中');
    fixtureData.controller.handleProgress(-1);
    assert.equal(fixtureData.controller.getSnapshot().pct, 0);
    fixtureData.controller.handleProgress(120);
    assert.equal(fixtureData.controller.getSnapshot().pct, 100);
    fixtureData.controller.handleProgress(Number.NaN);
    assert.equal(fixtureData.controller.getSnapshot().pct, 0);
    assert.equal(fixtureData.actionButton.disabled, true);
    assert.equal(fixtureData.progressRegion.hidden, false);
    assert.equal(fixtureData.progressBar.getAttribute('aria-valuenow'), '0');
    assert.match(fixtureData.statusNode.textContent, /下载中 0%/);
    resolveApply();
    await applying;
  });
});

test('failure unlocks retry and restart reports a toast', async () => {
  const fixtureData = fixture({
    check: async () => ({ hasUpdate: true, latest: '2.0.0' }),
  });
  await fixtureData.controller.check();
  fixtureData.controller.handleState('下载失败');
  assert.equal(fixtureData.controller.getSnapshot().busy, false);
  assert.equal(fixtureData.controller.getSnapshot().mode, 'idle');
  assert.equal(fixtureData.actionButton.disabled, false);
  assert.equal(fixtureData.warningNode.hidden, true);
  assert.match(fixtureData.statusNode.textContent, /下载失败/);
  fixtureData.controller.handleState('重启中');
  assert.deepEqual(fixtureData.toasts, ['✅ 更新完成，正在重启…']);
});

test('restart phase stays applying and disabled until the process exits', async () => {
  let resolveApply;
  const fixtureData = fixture({
    check: async () => ({ hasUpdate: true, latest: '2.0.0', current: '1.0.0' }),
    apply: () => new Promise((resolve) => { resolveApply = resolve; }),
  });
  await fixtureData.controller.check();
  const applying = fixtureData.actionButton.listeners.get('click')();
  fixtureData.controller.handleState('重启中');
  assert.equal(fixtureData.controller.getSnapshot().mode, 'applying');
  assert.equal(fixtureData.controller.getSnapshot().busy, true);
  assert.equal(fixtureData.actionButton.disabled, true);
  assert.equal(fixtureData.actionButton.textContent, '正在重启…');
  assert.equal(fixtureData.statusNode.textContent, '更新完成，正在重启…');
  assert.deepEqual(fixtureData.toasts, ['✅ 更新完成，正在重启…']);
  resolveApply();
  await applying;
});

test('UpdateToLatest rejection clears ready info and restores retry', async () => {
  const fixtureData = fixture({
    check: async () => ({ hasUpdate: true, latest: '2.0.0', current: '1.0.0' }),
    apply: async () => { throw new Error('download failed'); },
  });
  await fixtureData.controller.check();
  await fixtureData.actionButton.listeners.get('click')();
  assert.equal(fixtureData.controller.getSnapshot().mode, 'idle');
  assert.equal(fixtureData.controller.getSnapshot().busy, false);
  assert.equal(fixtureData.controller.getSnapshot().info, null);
  assert.equal(fixtureData.actionButton.disabled, false);
  assert.match(fixtureData.statusNode.textContent, /download failed/);
});

test('checking and applying suppress duplicate requests', async () => {
  let resolveCheck;
  const checking = fixture({
    check: () => new Promise((resolve) => { resolveCheck = resolve; }),
  });
  const firstCheck = checking.item.listeners.get('click')();
  await checking.item.listeners.get('click')();
  assert.equal(checking.calls.check, 1);
  resolveCheck({ hasUpdate: false, current: '1.0.0' });
  await firstCheck;

  let resolveApply;
  const applying = fixture({
    check: async () => ({ hasUpdate: true, latest: '2.0.0' }),
    apply: () => new Promise((resolve) => { resolveApply = resolve; }),
  });
  await applying.controller.check();
  const firstApply = applying.item.listeners.get('click')();
  await applying.item.listeners.get('click')();
  assert.equal(applying.calls.apply, 1);
  resolveApply();
  await firstApply;
});
