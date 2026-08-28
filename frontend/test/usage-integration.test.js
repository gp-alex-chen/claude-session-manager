import test from 'node:test';
import assert from 'node:assert/strict';

import { createPaneController } from '../src/panes/controller.js';
import { createAppState } from '../src/state/app-state.js';
import { createUsageController } from '../src/usage/controller.js';
import { createUsageView } from '../src/usage/view.js';

class ClassList {
  constructor() { this.values = new Set(); }
  add(...names) { names.forEach((name) => this.values.add(name)); }
  remove(...names) { names.forEach((name) => this.values.delete(name)); }
  toggle(name, force) {
    const next = force === undefined ? !this.values.has(name) : force;
    if (next) this.values.add(name);
    else this.values.delete(name);
    return next;
  }
  forEach(callback) { this.values.forEach(callback); }
}

class Node {
  constructor(tag = 'div') {
    this.tagName = tag.toUpperCase();
    this.children = [];
    this.parentNode = null;
    this.dataset = {};
    this.classList = new ClassList();
    this.listeners = new Map();
    this.attributes = new Map();
    this.hidden = false;
    this.disabled = false;
    this.value = '';
    this.textContent = '';
    this.id = '';
  }
  append(...children) { children.forEach((child) => this.appendChild(child)); }
  appendChild(child) {
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
  setAttribute(name, value) { this.attributes.set(name, String(value)); }
  getAttribute(name) { return this.attributes.get(name); }
  contains(target) {
    return target === this || this.children.some((child) => child.contains?.(target));
  }
}

function deferred() {
  let resolve;
  const promise = new Promise((res) => { resolve = res; });
  return { promise, resolve };
}

async function settle() {
  await Promise.resolve();
  await Promise.resolve();
}

test('pane assignment, usage requests, and per-pane rendering use the production chain', async () => {
  const state = createAppState();
  state.terminals.set('A', { token: 'A', labelText: 'A', dir: 'dir-a', exited: false });
  state.terminals.set('B', { token: 'B', labelText: 'B', dir: 'dir-b', exited: false });
  state.panes[0].token = 'A';
  state.panes[1].token = 'B';

  const terminalRoot = new Node('main');
  const statusBar = new Node('header');
  const documentRef = new Node('document');
  documentRef.createElement = (tag) => new Node(tag);
  const storage = new Map([['terminal-layout-mode', 'split-cols-2']]);
  const requests = [];
  const responses = new Map([
    ['A', deferred()],
    ['B', deferred()],
  ]);
  let usageView;
  let usageController;
  const paneController = createPaneController({
    state,
    terminalController: {
      mountSession() {},
      unmountSession() {},
      focusSession() {},
      resizeVisible() {},
    },
    terminalRoot,
    statusBar,
    documentRef,
    storageRef: {
      getItem: (key) => storage.get(key) || null,
      setItem: (key, value) => storage.set(key, value),
    },
    el: (tag, className, text) => {
      const node = new Node(tag);
      node.className = className || '';
      node.textContent = text || '';
      return node;
    },
    setStatus() {},
    onChange: () => {
      usageView?.render(state);
      void usageController?.refreshVisible({ force: false });
    },
  });
  usageView = createUsageView({
    surfaces: paneController.view.paneSurfaces(),
    documentRef,
    windowRef: new Node('window'),
  });
  usageController = createUsageController({
    state,
    GetUsageSummary: (sessionID, projectDir) => {
      requests.push({ sessionID, projectDir });
      return responses.get(sessionID).promise;
    },
    getVisibleAssignments: () => state.panes
      .filter((pane) => paneController.getVisiblePaneIds().includes(pane.id) && pane.token),
    render: (nextState) => usageView.render(nextState),
  });

  await paneController.initialize();
  assert.deepEqual(requests, [
    { sessionID: 'A', projectDir: 'dir-a' },
    { sessionID: 'B', projectDir: 'dir-b' },
  ]);

  responses.get('B').resolve({ project_found: true, session_found: true, session_total: { output_tokens: 20 } });
  responses.get('A').resolve({ project_found: true, session_found: true, session_total: { output_tokens: 10 } });
  await settle();

  const surfaces = paneController.view.paneSurfaces();
  assert.equal(surfaces[0].usageSummary.children[0].textContent, '会话 10');
  assert.equal(surfaces[1].usageSummary.children[0].textContent, '会话 20');
  assert.equal(surfaces[0].usageSummary.disabled, false);
  assert.equal(surfaces[1].usageSummary.disabled, false);
});
