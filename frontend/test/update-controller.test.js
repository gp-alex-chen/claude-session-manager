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
    this.classList = new FakeClassList();
    this.listeners = new Map();
    this.style = {};
    this.textContent = '';
    this.className = '';
  }
  append(...children) {
    children.forEach((child) => { child.parentNode = this; this.children.push(child); });
  }
  appendChild(child) { child.parentNode = this; this.children.push(child); return child; }
  addEventListener(name, callback) { this.listeners.set(name, callback); }
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
      node.className = className || '';
      node.textContent = text || '';
      return node;
    },
    setStatus: (message, kind) => statuses.push({ message, kind }),
    showToast: (message) => toasts.push(message),
    clampProgress,
  });
  controller.mount(menu);
  return {
    controller,
    menu,
    item: menu.children[1],
    statuses,
    toasts,
    calls,
    backend,
  };
}

test('check no-update and reject states return to retryable idle', async () => {
  const noUpdate = fixture({ check: async () => ({ hasUpdate: false, current: 'v0.3-wails-rc2-local' }) });
  await noUpdate.item.listeners.get('click')();
  assert.equal(noUpdate.controller.getSnapshot().mode, 'idle');
  assert.equal(noUpdate.calls.check, 1);
  assert.equal(noUpdate.item.classList.contains('disabled'), false);
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
  assert.match(replacement.children[1].textContent, /发现新版本 v2.0.0/);
  await replacement.children[1].listeners.get('click')();
  assert.equal(applied, 1);
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
    assert.equal(fixtureData.item.querySelectorAll('.upd-hint').length, 1);
    assert.equal(fixtureData.item.querySelectorAll('.upd-hint')[0].textContent, '下载中 0%');
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
  fixtureData.controller.handleState('重启中');
  assert.deepEqual(fixtureData.toasts, ['✅ 更新完成，正在重启…']);
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
