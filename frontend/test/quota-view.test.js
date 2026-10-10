import test from 'node:test';
import assert from 'node:assert/strict';
import { createQuotaView, expectedRemainingPercent } from '../src/quota/view.js';

class Node {
  constructor(tag = '') {
    this.tag = tag;
    this.children = [];
    this.attributes = new Map();
    this.listeners = new Map();
    this.classes = new Set();
    this.classList = { toggle: (name, enabled) => enabled ? this.classes.add(name) : this.classes.delete(name) };
    this.textContent = '';
  }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  setAttribute(name, value) { this.attributes.set(name, String(value)); }
  getAttribute(name) { return this.attributes.get(name); }
  addEventListener(name, callback) { this.listeners.set(name, callback); }
  removeEventListener(name, callback) {
    if (this.listeners.get(name) === callback) this.listeners.delete(name);
  }
  contains(target) { return this === target || this.children.some(node => node.contains(target)); }
  dispatch(name, event = {}) { this.listeners.get(name)?.({ target: this, ...event }); }
}

const now = Date.UTC(2026, 0, 1);
const windowAt = (minutes, fraction, usedPercent = 40) => ({
  windowMinutes: minutes, resetsAt: (now + minutes * 60_000 * fraction) / 1000, usedPercent,
  label: minutes === 300 ? '5 小时' : '周',
});
const input = windows => ({ provider: 'codex', snapshot: { status: 'available', windows }, loading: false, error: '' });
const textOf = node => [node.textContent, ...node.children.map(textOf)].join(' ');

function fixture() {
  const root = new Node();
  const documentRef = new Node();
  documentRef.createElement = tag => new Node(tag);
  documentRef.createElementNS = (_, tag) => new Node(tag);
  const timers = new Map();
  const intervals = [];
  let time = now;
  let nextTimer = 0;
  let refreshes = 0;
  const view = createQuotaView({ root, documentRef, nowFn: () => time, onRefresh: () => refreshes++,
    setIntervalFn: (callback, ms) => { intervals.push(ms); timers.set(++nextTimer, callback); return nextTimer; },
    clearIntervalFn: id => timers.delete(id),
  });
  const button = root.children[0];
  const panel = root.children[1];
  const chart = () => button.children[1].children[0];
  const byClass = name => chart().children.filter(node => node.getAttribute('class') === name);
  return { root, documentRef, view, button, panel, timers, intervals, chart, byClass,
    advance: ms => { time += ms; for (const callback of [...timers.values()]) callback(); },
    refreshes: () => refreshes,
  };
}

function closeTo(actual, expected) { assert.ok(Math.abs(Number(actual) - expected) < 1e-9, `${actual} != ${expected}`); }

test('steady use references remaining time for both five-hour and weekly allowance', () => {
  for (const minutes of [300, 10080]) {
    for (const fraction of [1, 0.75, 0.5, 0.25]) {
      closeTo(expectedRemainingPercent(windowAt(minutes, fraction), now), fraction * 100);
    }
    assert.equal(expectedRemainingPercent(windowAt(minutes, 2), now), 100);
    assert.equal(expectedRemainingPercent(windowAt(minutes, 0), now), null);
    assert.equal(expectedRemainingPercent(windowAt(minutes, -0.25), now), null);
  }
});

test('missing and invalid reset metadata cannot produce a pace marker', () => {
  const valid = windowAt(300, 0.5);
  for (const value of [undefined, null, 0, -1, NaN, Infinity, '300']) {
    assert.equal(expectedRemainingPercent({ ...valid, windowMinutes: value }, now), null);
    assert.equal(expectedRemainingPercent({ ...valid, resetsAt: value }, now), null);
  }
  assert.equal(expectedRemainingPercent(null, now), null);
  assert.equal(expectedRemainingPercent(valid, NaN), null);
});

test('used color grows clockwise from noon while the number shows remaining allowance', () => {
  const f = fixture();
  f.view.render(input([windowAt(300, 0.5, 0)]));
  assert.equal(f.chart().children.filter(node => node.tag === 'circle').length, 1);
  assert.equal(f.byClass('quota-short').find(node => node.tag === 'text').textContent, '100%');
  for (const used of [25, 50, 75, 100]) {
    f.view.render(input([windowAt(300, 0.5, used)]));
    const arc = f.byClass('quota-short').find(node => node.tag === 'circle');
    assert.equal(arc.getAttribute('stroke-dasharray'), `${used} 100`);
    closeTo(arc.getAttribute('stroke-dashoffset') ?? 0, 0);
    assert.equal(arc.getAttribute('transform'), 'rotate(-90 28 28)');
    assert.equal(f.byClass('quota-short').find(node => node.tag === 'text').textContent, `${100 - used}%`);
  }
  f.view.render(input([windowAt(300, 0.5, 100)]));
  assert.equal(f.chart().children.filter(node => node.tag === 'circle').length, 2);
  assert.equal(f.byClass('quota-short').find(node => node.tag === 'text').textContent, '0%');
  assert.equal(f.byClass('quota-pace-marker').length, 1);
});

test('pace tick passes noon, right, bottom and left in clockwise time order', () => {
  const f = fixture();
  for (const [fraction, x, y] of [[1, 28, 3], [0.75, 53, 28], [0.5, 28, 53], [0.25, 3, 28]]) {
    f.view.render(input([windowAt(300, fraction)]));
    const marker = f.byClass('quota-pace-marker')[0];
    closeTo((Number(marker.getAttribute('x1')) + Number(marker.getAttribute('x2'))) / 2, x);
    closeTo((Number(marker.getAttribute('y1')) + Number(marker.getAttribute('y2'))) / 2, y);
    assert.match(f.button.title, new RegExp(`均匀使用应已用 ${(1 - fraction) * 100}%`));
    assert.match(f.button.getAttribute('aria-label'), new RegExp(`均匀使用应已用 ${(1 - fraction) * 100}%`));
    assert.match(textOf(f.panel), new RegExp(`参考刻度：均匀使用应已用 ${(1 - fraction) * 100}%`));
  }
});

test('two rings use independent reset clocks and omit scoped windows from the meter', () => {
  const f = fixture();
  f.view.render(input([{ ...windowAt(10080, 0.1), scope: 'Opus' }, windowAt(10080, 0.25), windowAt(300, 0.75)]));
  const markers = f.byClass('quota-pace-marker');
  assert.equal(markers.length, 2);
  closeTo(markers[0].getAttribute('x1'), 51.7);
  closeTo(markers[0].getAttribute('y1'), 28);
  closeTo(markers[1].getAttribute('x1'), 9.3);
  closeTo(markers[1].getAttribute('y1'), 28);
  assert.equal(f.byClass('quota-short').find(node => node.tag === 'circle').getAttribute('r'), '25');
  assert.equal(f.byClass('quota-week').find(node => node.tag === 'circle').getAttribute('r'), '20');
});

test('invalid or expired windows and unavailable snapshots show no reference', () => {
  const f = fixture();
  f.view.start();
  for (const window of [windowAt(300, 0), { usedPercent: 20 }, { ...windowAt(300, 0.5), resetsAt: null }]) {
    f.view.render(input([window]));
    assert.equal(f.byClass('quota-pace-marker').length, 0);
    assert.doesNotMatch(f.button.title, /均匀使用/);
    assert.equal(f.timers.size, 0);
  }
  f.view.render({ provider: 'codex', snapshot: null, loading: true });
  assert.match(textOf(f.button), /…/);
  assert.equal(f.byClass('quota-pace-marker').length, 0);
  f.view.stop();
});

test('local minute ticks update pace without reading quota or changing actual remaining', () => {
  const f = fixture();
  f.view.render(input([windowAt(300, 0.5)]));
  assert.equal(f.timers.size, 0);
  f.view.start(); f.view.start();
  assert.equal(f.timers.size, 1);
  assert.deepEqual(f.intervals, [60_000]);
  const before = f.byClass('quota-pace-marker')[0].getAttribute('x1');
  f.advance(60_000);
  assert.notEqual(f.byClass('quota-pace-marker')[0].getAttribute('x1'), before);
  assert.match(f.button.title, /剩余 60%.*均匀使用应已用 50%/);
  f.advance(60_000);
  assert.match(f.button.title, /均匀使用应已用 51%/);
  assert.equal(f.refreshes(), 0);
  f.button.dispatch('click');
  assert.equal(f.panel.hidden, false);
  f.advance(60_000);
  assert.equal(f.panel.hidden, false);
  f.panel.children[1].dispatch('click');
  assert.equal(f.refreshes(), 1);
  f.advance(147 * 60_000);
  assert.equal(f.byClass('quota-pace-marker').length, 0);
  assert.equal(f.timers.size, 0);
  f.view.stop();
});

test('hiding, API mode and stopping release pace timers across provider changes and restart', () => {
  const f = fixture();
  f.view.start();
  f.view.render(input([windowAt(300, 0.5)]));
  f.view.render(input([windowAt(300, 0.5)]));
  assert.equal(f.timers.size, 1);
  f.view.render({ provider: null });
  assert.equal(f.root.hidden, true);
  assert.equal(f.timers.size, 0);
  f.view.render({ ...input([windowAt(10080, 0.25)]), provider: 'claude' });
  assert.match(f.button.title, /^Claude/);
  assert.equal(f.timers.size, 1);
  f.view.render({ provider: 'claude', snapshot: { status: 'unsupported', authMode: 'api', windows: [] } });
  assert.equal(f.timers.size, 0);
  f.view.render(input([windowAt(300, 0.5)]));
  f.view.stop(); f.view.stop();
  assert.equal(f.timers.size, 0);
  assert.equal(f.root.hidden, true);
  assert.equal(f.button.listeners.size, 0);
  assert.equal(f.documentRef.listeners.size, 0);
  f.view.start();
  assert.equal(f.timers.size, 0);
  f.view.render(input([windowAt(300, 0.5)]));
  assert.equal(f.timers.size, 1);
  f.view.stop();
});
