import test from 'node:test';
import assert from 'node:assert/strict';

import { createUsageView } from '../src/usage/view.js';

class Node {
  constructor() {
    this.children = [];
    this.listeners = new Map();
    this.attributes = new Map();
    this.hidden = false;
    this.className = '';
    this.textContent = '';
    this.id = '';
  }

  createElement() { return new Node(); }
  appendChild(child) { this.children.push(child); return child; }
  replaceChildren(...children) { this.children = children; }
  addEventListener(name, callback) { this.listeners.set(name, callback); }
  removeEventListener(name, callback) {
    if (this.listeners.get(name) === callback) this.listeners.delete(name);
  }
  setAttribute(name, value) { this.attributes.set(name, String(value)); }
  getAttribute(name) { return this.attributes.get(name); }
  contains(target) {
    return this.children.includes(target) || this.children.some((child) => child.contains?.(target));
  }
  dispatch(name, event = {}) { return this.listeners.get(name)?.({ target: this, ...event }); }
}

function makeFixture() {
  const documentRef = new Node();
  const windowRef = new Node();
  const summaryButton = new Node();
  const details = new Node();
  details.id = 'usage-details';
  const view = createUsageView({ summaryButton, details, documentRef, windowRef });
  return { documentRef, windowRef, summaryButton, details, view };
}

const complete = {
  project_found: true,
  session_found: true,
  project_request_count: 4,
  session_request_count: 2,
  project_total: {
    input_tokens: 1000,
    output_tokens: 200,
    cache_creation_input_tokens: 300,
    cache_read_input_tokens: 500,
  },
  session_total: { input_tokens: 10, output_tokens: 20, cache_read_input_tokens: 30 },
  latest: {
    input_tokens: 10,
    output_tokens: 20,
    cache_creation_input_tokens: 30,
    cache_read_input_tokens: 40,
    thinking_tokens: 5,
    cache_creation_5m_input_tokens: 11,
    cache_creation_1h_input_tokens: 12,
  },
};

test('complete summary renders compact chips and detail fields', () => {
  const fixture = makeFixture();
  fixture.view.render({ usageSummary: complete, usageLoading: false, usageStale: false });
  assert.deepEqual(fixture.summaryButton.children.map((child) => child.textContent), [
    '项目 2K', '缓存 27.8%', '会话 60',
  ]);
  const textOf = (node) => [node.textContent, ...(node.children || []).map(textOf)].join(' ');
  const detailText = textOf(fixture.details);
  assert.match(detailText, /Thinking（输出明细）/);
  assert.match(detailText, /缓存写入 5m/);
});

test('missing session and latest fields render dashes without fake zeros', () => {
  const fixture = makeFixture();
  fixture.view.render({
    usageSummary: { project_found: true, session_found: false, project_total: {} },
    usageLoading: false,
    usageStale: false,
  });
  assert.equal(fixture.summaryButton.children[2].textContent, '会话 —');
  const textOf = (node) => [node.textContent, ...(node.children || []).map(textOf)].join(' ');
  const detailText = textOf(fixture.details);
  assert.match(detailText, /累计 token\s+—/);
  assert.match(detailText, /总 token\s+—/);
});

test('loading, unavailable, and stale states are concise and path-free', () => {
  const fixture = makeFixture();
  fixture.view.render({ usageSummary: null, usageLoading: true, usageStale: false });
  assert.equal(fixture.summaryButton.children[0].textContent, '用量加载中…');
  fixture.view.render({ usageSummary: null, usageLoading: false, usageStale: false });
  assert.equal(fixture.summaryButton.children[0].textContent, '用量不可用');
  fixture.view.render({ usageSummary: complete, usageLoading: false, usageStale: true });
  assert.equal(fixture.summaryButton.children.at(-1).textContent, '数据较旧');
  assert.doesNotMatch(fixture.summaryButton.children.map((child) => child.textContent).join(' '), /[A-Z]:\\|\/Users/);
});

test('details open with aria state, stay open for internal clicks, and close on Escape/outside', () => {
  const fixture = makeFixture();
  fixture.view.render({ usageSummary: complete, usageLoading: false, usageStale: false });
  fixture.view.start();
  fixture.summaryButton.dispatch('click');
  assert.equal(fixture.details.hidden, false);
  assert.equal(fixture.summaryButton.getAttribute('aria-expanded'), 'true');
  const internal = fixture.details.children[0];
  fixture.documentRef.dispatch('click', { target: internal });
  assert.equal(fixture.details.hidden, false);
  fixture.windowRef.dispatch('keydown', { key: 'Escape' });
  assert.equal(fixture.details.hidden, true);
  fixture.summaryButton.dispatch('click');
  fixture.documentRef.dispatch('click', { target: new Node() });
  assert.equal(fixture.details.hidden, true);
});

test('view start/stop does not accumulate listeners', () => {
  const fixture = makeFixture();
  fixture.view.start();
  fixture.view.start();
  assert.equal(fixture.summaryButton.listeners.size, 1);
  assert.equal(fixture.windowRef.listeners.size, 1);
  assert.equal(fixture.documentRef.listeners.size, 1);
  fixture.view.stop();
  fixture.view.stop();
  assert.equal(fixture.summaryButton.listeners.size, 0);
  assert.equal(fixture.windowRef.listeners.size, 0);
  assert.equal(fixture.documentRef.listeners.size, 0);
});
