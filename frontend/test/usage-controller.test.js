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

function makeFixture(responses = [], options = {}) {
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
    getVisibleAssignments: options.getVisibleAssignments,
    nowFn: options.nowFn,
    render: options.render,
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

test('unpaired new token waits without sending an empty session ID', async () => {
  const fixture = makeFixture();
  realTerminal(fixture.state, 'new-1', 'C:/work');
  fixture.state.pendingNew.push({ token: 'new-1', dir: 'C:/work' });
  fixture.controller.start();

  fixture.controller.onActivate('new-1');
  assert.deepEqual(fixture.calls, []);
  await settle();
  assert.equal(fixture.state.usageProjectDir, 'C:/work');
  assert.equal(fixture.state.usageByToken.get('new-1').waiting, true);
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
  const paired = deferred();
  const fixture = makeFixture([paired]);
  realTerminal(fixture.state, 'new-1', 'dir-a');
  fixture.state.pendingNew.push({ token: 'new-1', dir: 'dir-a' });
  fixture.controller.start();
  fixture.controller.onActivate('new-1');
  assert.deepEqual(fixture.calls, []);
  fixture.state.newToReal.set('new-1', 'real-1');
  fixture.state.realToNew.set('real-1', 'new-1');
  fixture.state.sessionDirs.set('real-1', 'dir-a');
  fixture.controller.refreshActive();
  assert.deepEqual(fixture.calls, [
    { sessionID: 'real-1', projectDir: 'dir-a' },
  ]);

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

test('usage render observes the project cache from the same successful response', async () => {
  const response = deferred();
  const renderedProjectTotals = [];
  const fixture = makeFixture([response], {
    render: (state) => renderedProjectTotals.push(
      state.usageByProject.get('dir-a')?.project_total?.input_tokens,
    ),
  });
  realTerminal(fixture.state, 'A', 'dir-a');
  fixture.controller.start();
  fixture.controller.onActivate('A');
  response.resolve({ project_found: true, session_found: true, project_total: { input_tokens: 12 } });
  await settle();

  assert.equal(renderedProjectTotals.at(-1), 12);
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

test('refreshVisible requests each assigned real session independently', async () => {
  const first = deferred();
  const second = deferred();
  const fixture = makeFixture([first, second], {
    getVisibleAssignments: () => [{ token: 'A' }, { token: 'B' }],
  });
  realTerminal(fixture.state, 'A', 'dir-a');
  realTerminal(fixture.state, 'B', 'dir-b');
  fixture.controller.start();

  fixture.controller.refreshVisible();
  assert.deepEqual(fixture.calls, [
    { sessionID: 'A', projectDir: 'dir-a' },
    { sessionID: 'B', projectDir: 'dir-b' },
  ]);
  second.resolve({ project_found: true, session_found: true, session_total: { output_tokens: 20 } });
  first.resolve({ project_found: true, session_found: true, session_total: { output_tokens: 10 } });
  await settle();
  assert.equal(fixture.state.usageByToken.get('A').summary.session_total.output_tokens, 10);
  assert.equal(fixture.state.usageByToken.get('B').summary.session_total.output_tokens, 20);
});

test('refreshVisible deduplicates an in-flight request for the same token', () => {
  const response = deferred();
  const fixture = makeFixture([response], {
    getVisibleAssignments: () => [{ token: 'A' }],
  });
  realTerminal(fixture.state, 'A', 'dir-a');
  fixture.controller.start();

  fixture.controller.refreshVisible();
  fixture.controller.refreshVisible();

  assert.deepEqual(fixture.calls, [{ sessionID: 'A', projectDir: 'dir-a' }]);
});

test('cached usage becomes stale before its refresh request completes', async () => {
  const first = deferred();
  const second = deferred();
  let now = 1_000;
  const fixture = makeFixture([first, second], { nowFn: () => now });
  realTerminal(fixture.state, 'A', 'dir-a');
  fixture.controller.start();
  fixture.controller.onActivate('A');
  first.resolve({ project_found: true, session_found: true, session_total: { output_tokens: 10 } });
  await settle();

  now = 16_001;
  fixture.controller.refreshToken('A', { force: false });

  assert.equal(fixture.state.usageByToken.get('A').stale, true);
  assert.equal(fixture.state.usageByToken.get('A').loading, true);
  second.resolve({ project_found: true, session_found: true, session_total: { output_tokens: 11 } });
  await settle();
  assert.equal(fixture.state.usageByToken.get('A').stale, false);
});

test('an in-flight refresh still marks cached usage stale after the threshold', async () => {
  const first = deferred();
  const second = deferred();
  let now = 1_000;
  const fixture = makeFixture([first, second], {
    getVisibleAssignments: () => [{ token: 'A' }],
    nowFn: () => now,
  });
  realTerminal(fixture.state, 'A', 'dir-a');
  fixture.controller.start();
  fixture.controller.onActivate('A');
  first.resolve({ project_found: true, session_found: true, session_total: { output_tokens: 10 } });
  await settle();

  now = 6_001;
  fixture.controller.refreshToken('A', { force: false });
  now = 16_001;
  fixture.controller.refreshVisible();

  assert.equal(fixture.state.usageByToken.get('A').stale, true);
  assert.equal(fixture.calls.length, 2);
  second.resolve({ project_found: true, session_found: true, session_total: { output_tokens: 11 } });
  await settle();
  assert.equal(fixture.state.usageByToken.get('A').stale, false);
});

test('refreshVisible waits for an unpaired new token without calling the backend', () => {
  const fixture = makeFixture([], {
    getVisibleAssignments: () => [{ token: 'new-1' }],
  });
  realTerminal(fixture.state, 'new-1', 'dir-a');
  fixture.state.pendingNew.push({ token: 'new-1', dir: 'dir-a' });
  fixture.controller.start();

  fixture.controller.refreshVisible();

  assert.deepEqual(fixture.calls, []);
  assert.equal(fixture.state.usageByToken.get('new-1').waiting, true);
});

test('per-token failures do not mark another visible session stale', async () => {
  const first = deferred();
  const second = deferred();
  const fixture = makeFixture([first, second], {
    getVisibleAssignments: () => [{ token: 'A' }, { token: 'B' }],
  });
  realTerminal(fixture.state, 'A', 'dir-a');
  realTerminal(fixture.state, 'B', 'dir-b');
  fixture.controller.start();
  fixture.controller.refreshVisible();
  first.resolve({ project_found: true, session_found: true, session_total: { output_tokens: 10 } });
  await settle();
  second.reject(new Error('offline'));
  await settle();

  assert.equal(fixture.state.usageByToken.get('A').stale, false);
  assert.equal(fixture.state.usageByToken.get('A').summary.session_total.output_tokens, 10);
  assert.equal(fixture.state.usageByToken.get('B').error, 'offline');
  assert.equal(fixture.state.usageByToken.get('B').stale, false);
});

test('a successful older project request is retained when a newer one fails', async () => {
  const first = deferred();
  const second = deferred();
  const fixture = makeFixture([first, second], {
    getVisibleAssignments: () => [{ token: 'A' }, { token: 'B' }],
  });
  realTerminal(fixture.state, 'A', 'dir-a');
  realTerminal(fixture.state, 'B', 'dir-a');
  fixture.controller.start();
  fixture.controller.refreshVisible();

  second.reject(new Error('newer request offline'));
  await settle();
  first.resolve({ project_found: true, session_found: true, project_total: { input_tokens: 10 } });
  await settle();

  assert.equal(fixture.state.usageByProject.get('dir-a').project_total.input_tokens, 10);
});

test('pairing refreshes a waiting token with the real session identity', async () => {
  const paired = deferred();
  const fixture = makeFixture([paired], {
    getVisibleAssignments: () => [{ token: 'new-1' }],
  });
  realTerminal(fixture.state, 'new-1', 'dir-a');
  fixture.state.pendingNew.push({ token: 'new-1', dir: 'dir-a' });
  fixture.controller.start();
  fixture.controller.refreshVisible();

  fixture.state.newToReal.set('new-1', 'real-1');
  fixture.state.realToNew.set('real-1', 'new-1');
  fixture.state.sessionDirs.set('real-1', 'dir-a');
  fixture.controller.refreshToken('new-1', { force: true });
  assert.deepEqual(fixture.calls, [{ sessionID: 'real-1', projectDir: 'dir-a' }]);
  paired.resolve({ project_found: true, session_found: true, session_total: { output_tokens: 2 } });
  await settle();
  assert.equal(fixture.state.usageByToken.get('new-1').identity.sessionID, 'real-1');
  assert.equal(fixture.state.usageByToken.get('new-1').summary.session_total.output_tokens, 2);
});

test('removeToken invalidates its cache and late response', async () => {
  const response = deferred();
  const fixture = makeFixture([response], {
    getVisibleAssignments: () => [{ token: 'A' }],
  });
  realTerminal(fixture.state, 'A', 'dir-a');
  fixture.controller.start();
  fixture.controller.refreshVisible();
  fixture.controller.removeToken('A');
  response.resolve({ project_found: true, session_found: true, session_total: { output_tokens: 9 } });
  await settle();

  assert.equal(fixture.state.usageByToken.has('A'), false);
});

test('state usage containers are independent', () => {
  const first = createAppState();
  const second = createAppState();
  first.usageByProject.set('dir', { project_total: { input_tokens: 1 } });
  first.usageByToken.set('token', { summary: { project_total: { input_tokens: 1 } } });
  first.usageSummary = { project_total: { input_tokens: 1 } };
  assert.equal(second.usageByProject.size, 0);
  assert.equal(second.usageByToken.size, 0);
  assert.equal(second.usageSummary, null);
});
