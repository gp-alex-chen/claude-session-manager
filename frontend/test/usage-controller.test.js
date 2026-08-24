import test from 'node:test';
import assert from 'node:assert/strict';

import { createAppState } from '../src/state/app-state.js';
import { createUsageController } from '../src/usage/controller.js';

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function makeFixture(responses = []) {
  const state = createAppState();
  const calls = [];
  const intervals = [];
  const cleared = [];
  const controller = createUsageController({
    state,
    GetUsageSummary: (sessionID, projectDir) => {
      calls.push({ sessionID, projectDir });
      const response = responses.shift();
      return response?.promise || response || { project_total: { input_tokens: 1 } };
    },
    setIntervalFn: (callback, delay) => {
      const timer = { callback, delay };
      intervals.push(timer);
      return timer;
    },
    clearIntervalFn: (timer) => cleared.push(timer),
  });
  return { state, controller, calls, intervals, cleared };
}

async function settle() {
  await Promise.resolve();
  await Promise.resolve();
}

function realTerminal(state, token, dir) {
  state.terminals.set(token, { token, dir });
}

test('real session activation sends its real ID and project directory', async () => {
  const response = deferred();
  const fixture = makeFixture([response]);
  realTerminal(fixture.state, 'real-1', 'C:/work');
  fixture.controller.start();

  fixture.controller.onActivate('real-1');
  assert.deepEqual(fixture.calls, [{ sessionID: 'real-1', projectDir: 'C:/work' }]);
  response.resolve({ project_total: { input_tokens: 12 } });
  await settle();
  assert.equal(fixture.state.usageSummary.project_total.input_tokens, 12);
  assert.equal(fixture.state.usageSessionID, 'real-1');
});

test('unpaired new token sends empty session ID but keeps terminal directory', async () => {
  const response = deferred();
  const fixture = makeFixture([response]);
  realTerminal(fixture.state, 'new-1', 'C:/work');
  fixture.state.pendingNew.push({ token: 'new-1', dir: 'C:/work' });
  fixture.controller.start();

  fixture.controller.onActivate('new-1');
  assert.deepEqual(fixture.calls, [{ sessionID: '', projectDir: 'C:/work' }]);
  response.resolve({ project_total: { input_tokens: 2 } });
  await settle();
  assert.equal(fixture.state.usageProjectDir, 'C:/work');
});

test('switching A to B prevents an older response from overwriting B', async () => {
  const first = deferred();
  const second = deferred();
  const fixture = makeFixture([first, second]);
  realTerminal(fixture.state, 'A', 'dir-a');
  realTerminal(fixture.state, 'B', 'dir-b');
  fixture.controller.start();
  fixture.controller.onActivate('A');
  fixture.controller.onActivate('B');

  second.resolve({ project_total: { input_tokens: 20 } });
  await settle();
  first.resolve({ project_total: { input_tokens: 10 } });
  await settle();
  assert.equal(fixture.state.usageToken, 'B');
  assert.equal(fixture.state.usageSummary.project_total.input_tokens, 20);
  assert.equal(fixture.state.usageProjectDir, 'dir-b');
});

test('a newer request in the same scope wins over an older response', async () => {
  const first = deferred();
  const second = deferred();
  const fixture = makeFixture([first, second]);
  realTerminal(fixture.state, 'A', 'dir-a');
  fixture.controller.start();
  fixture.controller.onActivate('A');
  fixture.controller.refreshActive();

  second.resolve({ project_total: { input_tokens: 22 } });
  await settle();
  first.resolve({ project_total: { input_tokens: 11 } });
  await settle();
  assert.equal(fixture.state.usageSummary.project_total.input_tokens, 22);
});

test('stop prevents a late response from writing state', async () => {
  const response = deferred();
  const fixture = makeFixture([response]);
  realTerminal(fixture.state, 'A', 'dir-a');
  fixture.controller.start();
  fixture.controller.onActivate('A');
  fixture.controller.stop();
  response.resolve({ project_total: { input_tokens: 99 } });
  await settle();
  assert.equal(fixture.state.usageSummary, null);
  assert.equal(fixture.state.usageLoading, false);
});

test('failure preserves the same scope value and marks it stale', async () => {
  const first = deferred();
  const second = deferred();
  const fixture = makeFixture([first, second]);
  realTerminal(fixture.state, 'A', 'dir-a');
  fixture.controller.start();
  fixture.controller.onActivate('A');
  first.resolve({ project_total: { input_tokens: 7 } });
  await settle();
  fixture.controller.refreshActive();
  second.reject(new Error('offline'));
  await settle();
  assert.equal(fixture.state.usageSummary.project_total.input_tokens, 7);
  assert.equal(fixture.state.usageStale, true);
  assert.equal(fixture.state.usageError, 'offline');
});

test('start and stop are idempotent with one independent five-second timer', () => {
  const fixture = makeFixture();
  fixture.controller.start();
  fixture.controller.start();
  assert.deepEqual(fixture.intervals.map((timer) => timer.delay), [5000]);
  fixture.controller.stop();
  fixture.controller.stop();
  assert.equal(fixture.cleared.length, 1);
});

test('pairing a new token changes the request identity to the real session', async () => {
  const pending = deferred();
  const paired = deferred();
  const fixture = makeFixture([pending, paired]);
  realTerminal(fixture.state, 'new-1', 'dir-a');
  fixture.state.pendingNew.push({ token: 'new-1', dir: 'dir-a' });
  fixture.controller.start();
  fixture.controller.onActivate('new-1');
  fixture.state.newToReal.set('new-1', 'real-1');
  fixture.state.realToNew.set('real-1', 'new-1');
  fixture.state.sessionDirs.set('real-1', 'dir-a');
  fixture.controller.refreshActive();
  assert.deepEqual(fixture.calls, [
    { sessionID: '', projectDir: 'dir-a' },
    { sessionID: 'real-1', projectDir: 'dir-a' },
  ]);

  pending.resolve({ project_total: { input_tokens: 1 } });
  paired.resolve({ project_total: { input_tokens: 2 } });
  await settle();
  assert.equal(fixture.state.usageSessionID, 'real-1');
  assert.equal(fixture.state.usageSummary.project_total.input_tokens, 2);
});

test('no active token or directory becomes unavailable without a backend call', async () => {
  const fixture = makeFixture();
  fixture.controller.start();
  await fixture.controller.onActivate(null);
  assert.deepEqual(fixture.calls, []);
  assert.equal(fixture.state.usageSummary, null);
  assert.equal(fixture.state.usageLoading, false);
});

test('prefetches one representative per project and populates the project cache', async () => {
  const first = deferred();
  const second = deferred();
  const fixture = makeFixture([first, second]);
  fixture.controller.start();
  const pending = fixture.controller.prefetchProjects([
    { id: 'a-1', dir: 'dir-a' },
    { id: 'a-2', dir: 'dir-a' },
    { id: 'b-1', dir: 'dir-b' },
  ]);
  assert.deepEqual(fixture.calls, [
    { sessionID: 'a-1', projectDir: 'dir-a' },
    { sessionID: 'b-1', projectDir: 'dir-b' },
  ]);
  first.resolve({ project_found: true, project_total: { input_tokens: 10 } });
  second.resolve({ project_found: true, project_total: { input_tokens: 20 } });
  await pending;
  assert.equal(fixture.state.usageByProject.get('dir-a').project_total.input_tokens, 10);
  assert.equal(fixture.state.usageByProject.get('dir-b').project_total.input_tokens, 20);
});

test('active refresh wins over an older prefetch for the same project', async () => {
  const prefetch = deferred();
  const active = deferred();
  const fixture = makeFixture([prefetch, active]);
  realTerminal(fixture.state, 'active', 'dir-a');
  fixture.controller.start();
  fixture.controller.prefetchProjects([{ id: 'representative', dir: 'dir-a' }]);
  fixture.controller.onActivate('active');

  active.resolve({ project_found: true, session_found: true, project_total: { input_tokens: 20 } });
  await settle();
  prefetch.resolve({ project_found: true, project_total: { input_tokens: 10 } });
  await settle();
  assert.equal(fixture.state.usageByProject.get('dir-a').project_total.input_tokens, 20);
  assert.equal(fixture.state.usageSummary.project_total.input_tokens, 20);
});

test('prefetch failure keeps the existing project cache and stop blocks late writes', async () => {
  const failed = deferred();
  const fixture = makeFixture([failed]);
  fixture.state.usageByProject.set('dir-a', { project_found: true, project_total: { input_tokens: 8 } });
  fixture.controller.start();
  fixture.controller.prefetchProjects([{ id: 'a-1', dir: 'dir-a' }]);
  fixture.controller.stop();
  failed.resolve({ project_found: true, project_total: { input_tokens: 99 } });
  await settle();
  assert.equal(fixture.state.usageByProject.get('dir-a').project_total.input_tokens, 8);
});

test('state usage containers are independent', () => {
  const first = createAppState();
  const second = createAppState();
  first.usageByProject.set('dir', { project_total: { input_tokens: 1 } });
  first.usageSummary = { project_total: { input_tokens: 1 } };
  assert.equal(second.usageByProject.size, 0);
  assert.equal(second.usageSummary, null);
});
