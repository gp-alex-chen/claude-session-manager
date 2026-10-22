import test from 'node:test';
import assert from 'node:assert/strict';
import { createNotificationSettings } from '../src/notifications/settings.js';

class Node {
  constructor(tag, className, text) {
    this.tagName = tag; this.className = className; this.textContent = text || '';
    this.children = []; this.dataset = {}; this.listeners = new Map(); this.value = ''; this.checked = false;
  }
  append(...nodes) { this.children.push(...nodes); }
  appendChild(node) { this.children.push(node); return node; }
  setAttribute() {}
  addEventListener(name, fn) { this.listeners.set(name, fn); }
  click() { return this.listeners.get('click')?.(); }
}
const config = () => ({ enabled: false, program: 'python.exe', args: ['script with spaces.py', '中文'], workingDir: '', timeoutSeconds: 10,
  events: ['input_requested', 'permission_requested', 'task_completed'] });
function fixture(overrides = {}) {
  const root = new Node('section'); const calls = []; const intervals = new Map(); let id = 0;
  const backend = {
    GetNotificationConfig: async () => config(),
    SetNotificationConfig: async c => calls.push(['save', c]),
    TestNotification: async e => calls.push(['test', e]),
    GetNotificationStatus: async () => ({ running: false, pending: 0, dropped: 0, last: null }),
    ...overrides,
  };
  const view = createNotificationSettings({ backend, el: (...args) => new Node(...args),
    setIntervalFn: fn => { intervals.set(++id, fn); return id; }, clearIntervalFn: id => intervals.delete(id) });
  const find = key => { const visit = n => n.dataset.field === key ? n : n.children.map(visit).find(Boolean); return visit(root); };
  return { root, calls, intervals, view, find };
}

test('notification settings preserve argument boundaries and save before testing', async () => {
  const f = fixture(); await f.view.mount(f.root);
  assert.deepEqual(JSON.parse(f.find('args').value), ['script with spaces.py', '中文']);
  f.find('enabled').checked = true;
  f.find('testEvent').value = 'permission_requested';
  await f.find('test').click();
  assert.deepEqual(f.calls, [['save', { ...config(), enabled: true }], ['test', 'permission_requested']]);
  f.view.stop(); assert.equal(f.intervals.size, 0);
});

test('failed config reads cannot overwrite saved data or execute a program', async () => {
  const f = fixture({ GetNotificationConfig: async () => { throw Error('invalid JSON'); } });
  await f.view.mount(f.root);
  assert.equal(f.find('save').disabled, true);
  assert.equal(f.find('test').disabled, true);
  await f.find('test').click();
  assert.deepEqual(f.calls, []);
  f.view.stop();
});

test('save errors prevent execution and show failure; results are rendered as text', async () => {
  const f = fixture({ SetNotificationConfig: async () => { throw Error('cannot save'); },
    GetNotificationStatus: async () => ({ pending: 0, dropped: 2, last: { success: false, event: 'permission_requested', exitCode: 7, error: '<script>error</script>', finishedAt: 'now' } }) });
  await f.view.mount(f.root);
  assert.match(f.find('result').textContent, /<script>error<\/script>/);
  await f.find('test').click();
  assert.deepEqual(f.calls, []);
  assert.match(f.find('message').textContent, /cannot save/);
  f.view.stop();
});

test('a closed settings view ignores late loads and releases polling', async () => {
  let resolve;
  const f = fixture({ GetNotificationConfig: () => new Promise(r => { resolve = r; }) });
  const pending = f.view.mount(f.root); f.view.stop(); resolve(config()); await pending;
  assert.equal(f.find('save').disabled, true);
  assert.equal(f.intervals.size, 0);
});

test('argument JSON preserves empty values and rejects non-string arrays before saving', async () => {
  const f = fixture(); await f.view.mount(f.root);
  f.find('args').value = '["", " a b ", "&$()"]';
  await f.find('save').click();
  assert.deepEqual(f.calls[0][1].args, ['', ' a b ', '&$()']);
  f.find('args').value = '[1]'; await f.find('test').click();
  assert.equal(f.calls.length, 1);
  assert.match(f.find('message').textContent, /字符串数组/);
  f.view.stop();
});

test('closing while saving prevents a late test execution', async () => {
  let resolve;
  const f = fixture({ SetNotificationConfig: () => new Promise(r => { resolve = r; }) });
  await f.view.mount(f.root);
  const pending = f.find('test').click(); f.view.stop(); resolve(); await pending;
  assert.deepEqual(f.calls, []);
  assert.equal(f.intervals.size, 0);
});
