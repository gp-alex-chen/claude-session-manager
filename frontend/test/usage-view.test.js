import test from 'node:test';
import assert from 'node:assert/strict';

import { createUsageView } from '../src/usage/view.js';

class Node {
  constructor() {
    this.children = [];
    this.listeners = new Map();
    this.attributes = new Map();
    this.hidden = false;
    this.disabled = false;
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

function makeSurface(id) {
  const summary = new Node();
  summary.className = 'terminal-pane-usage-summary';
  const details = new Node();
  details.className = 'terminal-pane-usage-details';
  details.id = `terminal-pane-usage-details-${id}`;
  details.setAttribute('aria-hidden', 'true');
  return { id, usageSummary: summary, usageDetails: details };
}

function makeFixture(ids = ['pane-0']) {
  const documentRef = new Node();
  const windowRef = new Node();
  documentRef.createElement = () => new Node();
  const surfaces = ids.map(makeSurface);
  const view = createUsageView({ surfaces, documentRef, windowRef });
  return { documentRef, windowRef, surfaces, view };
}

function textOf(node) {
  return [node.textContent, ...(node.children || []).map(textOf)].join(' ');
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

test('per-pane view renders each session usage independently', () => {
  const fixture = makeFixture(['pane-0', 'pane-1']);
  fixture.view.render({
    panes: [{ id: 'pane-0', token: 'a' }, { id: 'pane-1', token: 'b' }],
    usageByToken: new Map([
      ['a', { summary: complete, loading: false, waiting: false, stale: false, error: null }],
      ['b', { summary: { ...complete, session_total: { output_tokens: 8 } }, loading: false, waiting: false, stale: false, error: null }],
    ]),
  });

  assert.equal(fixture.surfaces[0].usageSummary.children[0].textContent, '会话 100');
  assert.equal(fixture.surfaces[1].usageSummary.children[0].textContent, '会话 8');
  assert.equal(fixture.surfaces[0].usageSummary.getAttribute('aria-label'), '查看窗格 1 token 用量');
  assert.equal(fixture.surfaces[1].usageSummary.getAttribute('aria-label'), '查看窗格 2 token 用量');
  assert.equal(fixture.surfaces[0].usageSummary.disabled, false);
  assert.equal(fixture.surfaces[1].usageSummary.disabled, false);
  assert.equal(fixture.surfaces[0].usageDetails.hidden, true);
});

test('pane summary handles waiting, loading, unavailable, and stale states without paths', () => {
  const fixture = makeFixture(['pane-0', 'pane-1', 'pane-2', 'pane-3']);
  fixture.view.render({
    panes: [
      { id: 'pane-0', token: 'waiting' },
      { id: 'pane-1', token: 'loading' },
      { id: 'pane-2', token: 'failed' },
      { id: 'pane-3', token: 'stale' },
    ],
    usageByToken: new Map([
      ['waiting', { summary: null, waiting: true, loading: false, stale: false, error: null }],
      ['loading', { summary: null, waiting: false, loading: true, stale: false, error: null }],
      ['failed', { summary: null, waiting: false, loading: false, stale: false, error: 'C:\\private\\path' }],
      ['stale', { summary: complete, waiting: false, loading: false, stale: true, error: 'offline' }],
    ]),
  });

  assert.deepEqual(fixture.surfaces.map((surface) => surface.usageSummary.children[0].textContent), [
    '等待统计', '加载中…', '用量失败', '会话 100 · 较旧',
  ]);
  assert.equal(fixture.surfaces[2].usageSummary.disabled, true);
  assert.doesNotMatch(textOf(fixture.surfaces[2].usageDetails), /C:\\private/);
});

test('details show project and session metrics and only one pane can be open', () => {
  const fixture = makeFixture(['pane-0', 'pane-1']);
  fixture.view.render({
    panes: [{ id: 'pane-0', token: 'a' }, { id: 'pane-1', token: 'b' }],
    usageByToken: new Map([
      ['a', { summary: complete, loading: false, waiting: false, stale: false, error: null }],
      ['b', { summary: complete, loading: false, waiting: false, stale: false, error: null }],
    ]),
  });
  fixture.view.start();

  fixture.surfaces[0].usageSummary.dispatch('click');
  assert.equal(fixture.surfaces[0].usageDetails.hidden, false);
  assert.equal(fixture.surfaces[0].usageDetails.getAttribute('aria-hidden'), 'false');
  assert.equal(fixture.surfaces[0].usageSummary.getAttribute('aria-expanded'), 'true');
  assert.match(textOf(fixture.surfaces[0].usageDetails), /项目总量\s+2K/);
  assert.match(textOf(fixture.surfaces[0].usageDetails), /当前会话\s+100/);
  assert.match(textOf(fixture.surfaces[0].usageDetails), /缓存命中\s+27.8%/);

  fixture.surfaces[1].usageSummary.dispatch('click');
  assert.equal(fixture.surfaces[0].usageDetails.hidden, true);
  assert.equal(fixture.surfaces[1].usageDetails.hidden, false);
  const internal = fixture.surfaces[1].usageDetails.children[0];
  fixture.documentRef.dispatch('click', { target: internal });
  assert.equal(fixture.surfaces[1].usageDetails.hidden, false);
  fixture.windowRef.dispatch('keydown', { key: 'Escape' });
  assert.equal(fixture.surfaces[1].usageDetails.hidden, true);
  assert.equal(fixture.surfaces[1].usageDetails.getAttribute('aria-hidden'), 'true');
});

test('reassigning an open pane to another token closes its usage details', () => {
  const fixture = makeFixture(['pane-0']);
  fixture.view.render({
    panes: [{ id: 'pane-0', token: 'a' }],
    usageByToken: new Map([
      ['a', { summary: complete, loading: false, waiting: false, stale: false, error: null }],
      ['b', { summary: complete, loading: false, waiting: false, stale: false, error: null }],
    ]),
  });
  fixture.view.start();
  fixture.surfaces[0].usageSummary.dispatch('click');
  assert.equal(fixture.surfaces[0].usageDetails.hidden, false);

  fixture.view.render({
    panes: [{ id: 'pane-0', token: 'b' }],
    usageByToken: new Map([
      ['a', { summary: complete, loading: false, waiting: false, stale: false, error: null }],
      ['b', { summary: complete, loading: false, waiting: false, stale: false, error: null }],
    ]),
  });

  assert.equal(fixture.surfaces[0].usageDetails.hidden, true);
  assert.equal(fixture.surfaces[0].usageSummary.getAttribute('aria-expanded'), 'false');
});

test('empty panes disable usage details and external clicks close the popover', () => {
  const fixture = makeFixture(['pane-0']);
  fixture.view.render({ panes: [{ id: 'pane-0', token: null }], usageByToken: new Map() });
  fixture.view.start();
  assert.equal(fixture.surfaces[0].usageSummary.disabled, true);
  fixture.surfaces[0].usageSummary.dispatch('click');
  assert.equal(fixture.surfaces[0].usageDetails.hidden, true);
  fixture.view.render({
    panes: [{ id: 'pane-0', token: 'a' }],
    usageByToken: new Map([['a', { summary: complete, loading: false, waiting: false, stale: false, error: null }]]),
  });
  fixture.surfaces[0].usageSummary.dispatch('click');
  fixture.documentRef.dispatch('click', { target: new Node() });
  assert.equal(fixture.surfaces[0].usageDetails.hidden, true);
});

test('view start and stop do not accumulate listeners', () => {
  const fixture = makeFixture(['pane-0', 'pane-1']);
  fixture.view.start();
  fixture.view.start();
  assert.equal(fixture.surfaces[0].usageSummary.listeners.size, 1);
  assert.equal(fixture.surfaces[1].usageSummary.listeners.size, 1);
  assert.equal(fixture.windowRef.listeners.size, 1);
  assert.equal(fixture.documentRef.listeners.size, 1);
  fixture.view.stop();
  fixture.view.stop();
  assert.equal(fixture.surfaces[0].usageSummary.listeners.size, 0);
  assert.equal(fixture.surfaces[1].usageSummary.listeners.size, 0);
  assert.equal(fixture.windowRef.listeners.size, 0);
  assert.equal(fixture.documentRef.listeners.size, 0);
});
