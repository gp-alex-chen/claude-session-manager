import test from 'node:test';
import assert from 'node:assert/strict';
import { createAppState } from '../src/state/app-state.js';
import { createAgentController } from '../src/agents/controller.js';

function badgeNode(id, provider) {
  const classes = new Set(['badge', 'idle']);
  let text = '●';
  let html = '';
  return {
    dataset: { id, provider }, attributes: new Map(), title: '',
    classList: { contains: (name) => classes.has(name), add: (...names) => names.forEach((name) => classes.add(name)), remove: (...names) => names.forEach((name) => classes.delete(name)) },
    setAttribute(name, value) { this.attributes.set(name, String(value)); },
    get textContent() { return text; },
    set textContent(value) { text = value; html = ''; },
    get innerHTML() { return html; },
    set innerHTML(value) { html = value; text = ''; },
  };
}

function fixture(provider) {
  const state = createAppState();
  const badge = badgeNode('session', provider);
  const item = { dataset: { id: 'session' }, querySelector: () => badge };
  const listRoot = {
    querySelector: () => item,
    querySelectorAll: (selector) => selector === '.session-item' ? [item] : [badge],
  };
  state.sessionProviders.set('session', provider);
  state.agentProviders.set(provider, { name: provider === 'claude' ? 'Claude' : 'Codex', hasStatus: true });
  const timeouts = [];
  const controller = createAgentController({
    state, listRoot, documentRef: null, GetAgents: async () => [], DebugLog() {}, setStatus() {},
    setTimeoutFn: (callback) => { timeouts.push(callback); return timeouts.length; }, clearTimeoutFn() {},
  });
  return { state, badge, controller, timeouts };
}

test('open and idle states use a provider SVG that inherits the status color', () => {
  for (const provider of ['claude', 'codex']) {
    const f = fixture(provider);
    f.controller.repaintBadge(f.badge);
    assert.match(f.badge.innerHTML, /<svg/);
    assert.match(f.badge.innerHTML, /fill="currentColor"/);
    assert.doesNotMatch(f.badge.innerHTML, /#D97757/);
    assert.ok(f.badge.classList.contains('idle'));
    assert.ok(f.badge.classList.contains('provider-status-icon'));
    assert.match(f.badge.attributes.get('aria-label'), /未打开/);
    f.state.runningAgents.set('session', { kind: 'interactive', state: 'idle' });
    f.controller.repaintBadge(f.badge);
    assert.match(f.badge.innerHTML, /<svg/);
    assert.ok(f.badge.classList.contains('open'));
    assert.match(f.badge.attributes.get('aria-label'), /已打开/);
  }
});

test('Codex unknown task status keeps its open icon and explicit unknown status', () => {
  const f = fixture('codex');
  f.state.terminals.set('session', { exited: false });
  f.controller.repaintBadge(f.badge);
  assert.match(f.badge.innerHTML, /<svg/);
  assert.ok(f.badge.classList.contains('open'));
  assert.match(f.badge.title, /任务状态未知/);
});

test('status transitions replace SVG with the existing task glyphs and restore the icon', () => {
  const f = fixture('codex');
  f.state.runningAgents.set('session', { kind: 'interactive', state: 'idle' });
  f.controller.repaintBadge(f.badge);
  for (const [agent, glyph, statusClass] of [
    [{ state: 'working' }, '▁', 'ecg'],
    [{ state: 'blocked', waitingFor: 'userInput' }, '⚠', 'blocked'],
    [{ state: 'failed' }, '⚠', 'blocked'],
    [{ state: 'interrupted' }, '■', 'blocked'],
    [{ state: 'idle', kind: 'background' }, '●', 'running'],
  ]) {
    f.state.runningAgents.set('session', agent);
    f.controller.repaintBadge(f.badge);
    assert.equal(f.badge.textContent, glyph);
    assert.equal(f.badge.innerHTML, '');
    assert.ok(f.badge.classList.contains(statusClass));
    assert.equal(f.badge.classList.contains('provider-status-icon'), false);
  }
  f.state.runningAgents.delete('session');
  f.controller.repaintBadge(f.badge);
  assert.match(f.badge.innerHTML, /<svg/);
  assert.ok(f.badge.classList.contains('idle'));
});

test('completion and unread animations keep their existing glyphs without an icon overlay', () => {
  const f = fixture('claude');
  f.state.runningAgents.set('session', { kind: 'interactive', state: 'idle' });
  f.controller.repaintBadge(f.badge);
  f.controller.markEnded('session', 'Title', true);
  assert.equal(f.badge.textContent, '─');
  assert.equal(f.badge.innerHTML, '');
  assert.equal(f.badge.classList.contains('provider-status-icon'), false);
  assert.ok(f.badge.classList.contains('ended-anim'));
  f.timeouts.at(-1)();
  assert.equal(f.badge.textContent, '●');
  assert.ok(f.badge.classList.contains('unread'));
  f.state.unreadSessions.clear();
  f.controller.renderUnreadMarks();
  assert.match(f.badge.innerHTML, /<svg/);
  assert.ok(f.badge.classList.contains('open'));
});
