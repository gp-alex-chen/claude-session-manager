import test from 'node:test';
import assert from 'node:assert/strict';
import { createAppState } from '../src/state/app-state.js';
import { createCompletionController } from '../src/notifications/controller.js';

function fixture(update) {
  const state = createAppState();
  const calls = [];
  const notifications = [];
  let focused = true;
  const listeners = new Map();
  const doc = { visibilityState: 'visible', hasFocus: () => focused,
    addEventListener: (event, cb) => listeners.set(event, cb), removeEventListener: event => listeners.delete(event) };
  const win = { addEventListener: (event, cb) => listeners.set(event, cb), removeEventListener: event => listeners.delete(event) };
  const controller = createCompletionController({ state, documentRef: doc, windowRef: win,
    SetNotificationAttention: async (...args) => { notifications.push(args); },
    SetTaskbarStatus: async (...args) => { calls.push(args); await update?.(...args); } });
  controller.start();
  return { state, calls, notifications, controller, doc, listeners, focus: value => { focused = value; } };
}
const flush = async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); };

test('completion badge counts sessions once and acknowledges only the focused visible session', async () => {
  const f = fixture();
  f.state.activeToken = 'a';
  f.controller.complete('a');
  f.controller.complete('b');
  f.controller.complete('b');
  f.controller.complete('c');
  assert.equal(f.controller.count(), 2);
  f.state.activeToken = 'b'; f.controller.activate('b');
  assert.equal(f.controller.count(), 1);
  f.controller.dismiss('c'); await flush();
  assert.equal(f.controller.count(), 0);
  assert.equal(f.calls.at(-1)[0], 0);
  f.controller.stop();
});

test('minimized or unfocused current sessions count and focus acknowledges only the current session', () => {
  const f = fixture(); f.state.activeToken = 'a';
  f.focus(false); f.controller.complete('a'); f.controller.complete('b');
  assert.equal(f.controller.count(), 2);
  f.controller.activate('a'); assert.equal(f.controller.count(), 2);
  f.focus(true); f.doc.visibilityState = 'hidden'; f.controller.activate('a');
  assert.equal(f.controller.count(), 2);
  f.doc.visibilityState = 'visible'; f.listeners.get('focus')();
  assert.equal(f.controller.count(), 1);
  f.controller.stop(); assert.equal(f.listeners.size, 0);
});

test('native identities deduplicate before binding and preserve the previous session when a runtime changes thread', () => {
  const f = fixture();
  f.controller.complete('new-codex-one', 'codex:a');
  f.state.newToReal.set('new-codex-one', 'codex:a');
  f.state.realToNew.set('codex:a', 'new-codex-one');
  f.controller.complete('codex:a', 'codex:a');
  assert.equal(f.controller.count(), 1);
  f.state.newToReal.set('new-codex-one', 'codex:b');
  f.state.realToNew.delete('codex:a');
  f.state.realToNew.set('codex:b', 'new-codex-one');
  f.controller.complete('codex:b', 'codex:b');
  f.state.activeToken = 'new-codex-one'; f.controller.activate('new-codex-one');
  assert.equal(f.controller.count(), 1);
  f.state.activeToken = 'codex:a'; f.controller.activate('codex:a');
  assert.equal(f.controller.count(), 0);
  f.controller.stop();
});

test('badge writes are serialized and coalesce to the latest count even while the first call is delayed', async () => {
  let resolve; let inFlight = 0; let maximum = 0;
  const blocked = new Promise(r => { resolve = r; });
  const f = fixture(async () => { maximum = Math.max(maximum, ++inFlight); await blocked; inFlight--; });
  f.controller.complete('a'); f.controller.complete('b'); f.controller.dismiss('a'); f.controller.dismiss('b');
  f.controller.stop(); resolve(); await flush();
  assert.equal(maximum, 1);
  assert.equal(f.calls.at(-1)[0], 0);
});

test('notification aggregate includes ordinary unread endings and deduplicates mapped identities', async () => {
  const f = fixture(); await flush();
  f.state.newToReal.set('runtime', 'canonical');
  f.state.realToNew.set('canonical', 'runtime');
  f.state.unreadSessions.add('runtime'); f.state.unreadSessions.add('canonical');
  f.controller.sync(); await flush();
  assert.equal(f.notifications.at(-1)[0], 1);
  assert.equal(f.calls.at(-1)[0], 0);
  f.controller.complete('canonical'); await flush();
  assert.equal(f.notifications.at(-1)[0], 1);
  f.state.activeToken = 'runtime'; f.controller.activate('runtime'); await flush();
  assert.deepEqual(f.notifications.at(-1).slice(0, 2), [0, false]);
  f.controller.stop();
});

test('an in-flight notification cannot overtake a later clear or shutdown', async () => {
  const state = createAppState(); const writes = []; let release;
  const delayed = new Promise(resolve => { release = resolve; });
  const controller = createCompletionController({ state, documentRef: { hasFocus: () => false },
    SetNotificationAttention: async (...args) => { writes.push(args); if (writes.length === 1) await delayed; } });
  controller.start(); controller.complete('done'); controller.dismiss('done'); controller.stop();
  release(); await flush();
  assert.deepEqual(writes.at(-1), [0, false, true]);
});

test('rejected notification still drains the latest clear and does not block the taskbar', async () => {
  const state = createAppState(); const notifications = []; const badges = []; let reject;
  const delayed = new Promise((_, fail) => { reject = fail; });
  const controller = createCompletionController({ state,
    SetNotificationAttention: async (...args) => { notifications.push(args); if (notifications.length === 1) await delayed; },
    SetTaskbarStatus: async (...args) => badges.push(args) });
  controller.start(); controller.complete('done'); controller.dismiss('done'); controller.stop();
  reject(Error('notification unavailable')); await flush();
  assert.equal(notifications.length, 2);
  assert.deepEqual(notifications.at(-1), [0, false, true]);
  assert.equal(badges.length, 2);
});

test('rejected taskbar call still drains newer notification snapshots', async () => {
  const state = createAppState(); const notifications = []; const badges = []; let reject;
  const delayed = new Promise((_, fail) => { reject = fail; });
  const controller = createCompletionController({ state,
    SetNotificationAttention: async (...args) => notifications.push(args),
    SetTaskbarStatus: async (...args) => { badges.push(args); if (badges.length === 1) await delayed; } });
  controller.start(); await flush();
  controller.complete('done'); controller.stop(); reject(Error('taskbar unavailable')); await flush();
  assert.deepEqual(notifications.at(-1), [0, false, true]);
  assert.equal(badges.length, 2);
});
