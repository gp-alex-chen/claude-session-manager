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

function textOf(node) {
  return [node.textContent, ...(node.children || []).map(textOf)].join(' ');
}

function findByClass(node, className) {
  if (node.className?.split?.(' ').includes(className)) return node;
  for (const child of node.children || []) {
    const match = findByClass(child, className);
    if (match) return match;
  }
  return null;
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
    thinking_tokens: 7,
    cache_creation_5m_input_tokens: 13,
    cache_creation_1h_input_tokens: 17,
  },
  session_total: {
    input_tokens: 10,
    output_tokens: 20,
    cache_creation_input_tokens: 40,
    cache_read_input_tokens: 30,
    thinking_tokens: 6,
  },
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

test('complete summary renders hero metrics, project/session comparison, and request grid', () => {
  const fixture = makeFixture();
  fixture.view.render({ usageSummary: complete, usageLoading: false, usageStale: false });
  assert.deepEqual(fixture.summaryButton.children.map((child) => child.textContent), [
    '项目 2K', '缓存 27.8%', '会话 100',
  ]);
  const detailText = textOf(fixture.details);
  assert.match(detailText, /Token 用量/);
  assert.match(detailText, /项目总量\s+2K/);
  assert.match(detailText, /当前会话\s+100/);
  assert.match(detailText, /缓存命中\s+27.8%/);
  assert.match(detailText, /累计指标\s+项目\s+会话/);
  assert.match(detailText, /新输入\s+1K\s+10/);
  assert.match(detailText, /输出\s+200\s+20/);
  assert.match(detailText, /思考 Token\s+7\s+6/);
  assert.match(detailText, /思考 Token 已包含在输出中/);
  assert.match(detailText, /本轮请求/);
  assert.match(detailText, /总量\s+100\s+新输入\s+10\s+输出\s+20/);
  assert.match(detailText, /缓存层级 · 5m 11 · 1h 12/);
  assert.equal(findByClass(fixture.details, 'usage-hero-grid')?.children.length, 3);
  assert.equal(findByClass(fixture.details, 'usage-compare-table')?.children.length, 2);
  assert.equal(findByClass(fixture.details, 'usage-request-grid')?.children.length, 6);
});

test('missing session and latest fields render dashes without fake zeros', () => {
  const fixture = makeFixture();
  fixture.view.render({
    usageSummary: { project_found: true, session_found: false, project_total: {} },
    usageLoading: false,
    usageStale: false,
  });
  assert.equal(fixture.summaryButton.children[2].textContent, '会话 —');
  const detailText = textOf(fixture.details);
  assert.match(detailText, /当前会话\s+—/);
  assert.match(detailText, /请求数\s+0\s+—/);
  assert.match(detailText, /输出\s+0\s+—/);
  assert.match(detailText, /本轮请求/);
  assert.match(detailText, /缓存层级 · 5m — · 1h —/);
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
