import test from 'node:test';
import assert from 'node:assert/strict';

import { createAppState } from '../src/state/app-state.js';
import { createAgentController } from '../src/agents/controller.js';

function fixture(fetch = async () => []) {
  const state = createAppState();
  const notices = [];
  const statuses = [];
  const logs = [];
  const intervals = [];
  const cleared = [];
  let nextTimer = 0;
  const controller = createAgentController({
    state,
    GetAgents: fetch,
    DebugLog: (message) => logs.push(message),
    NotifyBeep: () => { notices.push('beep'); },
    setStatus: (message, kind) => statuses.push({ message, kind }),
    listRoot: null,
    refreshFoldState: () => {},
    setIntervalFn: (callback, delay) => {
      const timer = { id: ++nextTimer, callback, delay };
      intervals.push(timer);
      return timer;
    },
    clearIntervalFn: (timer) => cleared.push(timer),
    setTimeoutFn: (callback) => {
      callback();
      return null;
    },
    clearTimeoutFn: () => {},
  });
  return { state, controller, notices, statuses, logs, intervals, cleared };
}

const agent = (sessionId, fields = {}) => ({ sessionId, ...fields });

test('Codex waits, failures, interruptions and disconnects never signal success', () => {
  const f = fixture();
  f.state.agentProviders.set('codex', { hasStatus: true });
  f.state.sessionProviders.set('codex:id', 'codex');
  const snapshot = (state, fields = {}) => [agent('codex:id', { provider: 'codex', kind: 'interactive', state, ...fields })];
  f.controller.applyAgents(snapshot('working', { turnId: 'one', turnStatus: 'inProgress' }));
  for (const state of ['blocked', 'failed', 'interrupted', 'unknown']) {
    f.controller.applyAgents(snapshot(state, { turnId: 'one', turnStatus: state }));
    assert.equal(f.notices.length, 0);
  }
  f.controller.applyAgents([]);
  assert.equal(f.notices.length, 0);
  f.controller.applyAgents(snapshot('idle', { turnId: 'two', turnStatus: 'completed' }));
  f.controller.applyAgents(snapshot('idle', { turnId: 'two', turnStatus: 'completed' }));
  assert.equal(f.notices.length, 1);
  f.controller.applyAgents(snapshot('idle', { turnId: 'three', turnStatus: 'completed' }));
  assert.equal(f.notices.length, 2);
});

test('Codex authoritative status follows temporary runtime token without adopting history', () => {
  const f = fixture();
  f.state.agentProviders.set('codex', { hasStatus: true });
  f.state.sessionProviders.set('new-codex-1', 'codex');
  f.controller.applyAgents([agent('codex:uuid', { provider: 'codex', runtimeToken: 'new-codex-1', kind: 'interactive', state: 'working' })]);
  assert.equal(f.controller.classifyAgent('new-codex-1'), 'working');
  assert.equal(f.state.newToReal.size, 0);
});

test('Codex formal identity keeps status and completion notifications on its canonical row', () => {
  const f = fixture();
  const token='new-codex-formal',id='codex:uuid';
  f.state.agentProviders.set('codex',{hasStatus:true});
  f.state.sessionProviders.set(id,'codex');
  f.state.newToReal.set(token,id);f.state.realToNew.set(id,token);
  f.controller.applyAgents([agent(id,{provider:'codex',runtimeToken:token,kind:'interactive',state:'working',turnId:'one',turnStatus:'inProgress'})]);
  assert.equal(f.controller.classifyAgent(id),'working');
  f.controller.applyAgents([agent(id,{provider:'codex',runtimeToken:token,kind:'interactive',state:'idle',turnId:'one',turnStatus:'completed'})]);
  assert.equal(f.notices.length,1);
  assert.equal(f.state.unreadSessions.has(id),true);
  assert.equal(f.state.unreadSessions.has(token),false);
});

test('Codex terminal lifecycle never claims task completion or working state', () => {
  const f = fixture();
  f.state.sessionProviders.set('codex:id', 'codex');
  f.state.terminals.set('codex:id', { exited: false });
  assert.equal(f.controller.classifyAgent('codex:id'), 'unknown-open');
  f.controller.applyAgents([]);
  assert.equal(f.notices.length, 0);
  f.state.terminals.get('codex:id').exited = true;
  assert.equal(f.controller.classifyAgent('codex:id'), 'idle');
});

test('classifyAgent covers idle, working, blocked, open, and background', () => {
  const { state, controller } = fixture();
  state.runningAgents.set('working', agent('working', { state: 'working' }));
  state.runningAgents.set('blocked', agent('blocked', { state: 'blocked' }));
  state.runningAgents.set('open', agent('open', { kind: 'interactive', status: 'idle' }));
  state.runningAgents.set('bg', agent('bg', { kind: 'background', status: 'idle' }));
  state.runningAgents.set('done', agent('done', { state: 'done' }));

  assert.equal(controller.classifyAgent('missing'), 'idle');
  assert.equal(controller.classifyAgent('working'), 'working');
  assert.equal(controller.classifyAgent('blocked'), 'blocked');
  assert.equal(controller.classifyAgent('open'), 'open');
  assert.equal(controller.classifyAgent('bg'), 'bg');
  assert.equal(controller.classifyAgent('done'), 'idle');
});

test('first snapshot establishes a baseline without notifying', () => {
  const fixtureData = fixture();
  fixtureData.controller.applyAgents([agent('job', { kind: 'background', state: 'working' })]);
  assert.deepEqual(fixtureData.notices, []);
  assert.equal(fixtureData.state.previousRunningAgents.get('job').state, 'working');
});

test('done baseline and repeated done or disappearance stay silent', () => {
  const fixtureData = fixture();
  fixtureData.controller.applyAgents([agent('done', { state: 'done' })]);
  fixtureData.controller.applyAgents([agent('done', { state: 'done' })]);
  fixtureData.controller.applyAgents([]);
  assert.equal(fixtureData.notices.length, 0);
});

test('background completion or disappearance notifies only once', () => {
  const fixtureData = fixture();
  fixtureData.controller.applyAgents([agent('job', { kind: 'background', state: 'working' })]);
  fixtureData.controller.applyAgents([agent('job', { kind: 'background', state: 'done' })]);
  fixtureData.controller.applyAgents([agent('job', { kind: 'background', state: 'done' })]);
  fixtureData.controller.applyAgents([]);

  assert.equal(fixtureData.notices.length, 1);
  assert.match(fixtureData.statuses[0].message, /任务完成/);
  assert.equal(fixtureData.state.unreadSessions.has('job'), true);

  const disappeared = fixture();
  disappeared.controller.applyAgents([agent('gone', { kind: 'background', state: 'working' })]);
  disappeared.controller.applyAgents([]);
  assert.equal(disappeared.notices.length, 1);
  assert.match(disappeared.statuses[0].message, /任务完成/);
});

test('interactive busy-to-idle is completion, idle-to-gone is session end', () => {
  const busy = fixture();
  busy.controller.applyAgents([agent('interactive', { kind: 'interactive', status: 'busy' })]);
  busy.controller.applyAgents([agent('interactive', { kind: 'interactive', status: 'idle' })]);
  assert.match(busy.statuses[0].message, /任务完成/);

  const idle = fixture();
  idle.controller.applyAgents([agent('interactive', { kind: 'interactive', status: 'idle' })]);
  idle.controller.applyAgents([]);
  assert.match(idle.statuses[0].message, /会话结束/);
});

test('closed sessions are skipped and re-busy clears the completion latch', () => {
  const closed = fixture();
  closed.state.closedTokens.add('closed');
  closed.controller.applyAgents([agent('closed', { state: 'working' })]);
  closed.controller.applyAgents([agent('closed', { state: 'done' })]);
  assert.equal(closed.notices.length, 0);

  const repeated = fixture();
  repeated.controller.applyAgents([agent('job', { state: 'working' })]);
  repeated.controller.applyAgents([agent('job', { state: 'done' })]);
  repeated.controller.applyAgents([agent('job', { state: 'working' })]);
  repeated.controller.applyAgents([agent('job', { state: 'done' })]);
  assert.equal(repeated.notices.length, 2);
});

test('reopening after a skipped done does not replay stale completion', () => {
  const fixtureData = fixture();
  fixtureData.controller.applyAgents([agent('job', { state: 'working' })]);
  fixtureData.state.closedTokens.add('job');
  fixtureData.controller.applyAgents([agent('job', { state: 'done' })]);
  fixtureData.state.closedTokens.delete('job');
  fixtureData.controller.applyAgents([agent('job', { state: 'done' })]);
  assert.equal(fixtureData.notices.length, 0);

  fixtureData.controller.applyAgents([agent('job', { state: 'working' })]);
  fixtureData.controller.applyAgents([agent('job', { state: 'done' })]);
  assert.equal(fixtureData.notices.length, 1);
});

test('active sessions avoid unread but still notify', () => {
  const fixtureData = fixture();
  fixtureData.state.activeToken = 'job';
  fixtureData.controller.applyAgents([agent('job', { state: 'working' })]);
  fixtureData.controller.applyAgents([agent('job', { state: 'done' })]);
  assert.equal(fixtureData.state.unreadSessions.has('job'), false);
  assert.deepEqual(fixtureData.notices, ['beep']);
  assert.match(fixtureData.statuses[0].message, /任务完成/);
});

test('GetAgents rejection preserves the existing snapshot', async () => {
  const fixtureData = fixture(async () => { throw new Error('offline'); });
  fixtureData.controller.applyAgents([agent('job', { state: 'working' })]);
  await fixtureData.controller.refreshAgents();
  assert.equal(fixtureData.state.runningAgents.get('job').state, 'working');
  assert.equal(fixtureData.notices.length, 0);
});

test('start is idempotent and stop clears both timers', () => {
  const fixtureData = fixture();
  fixtureData.controller.start();
  fixtureData.controller.start();
  assert.deepEqual(fixtureData.intervals.map((timer) => timer.delay), [120, 30000]);
  fixtureData.controller.stop();
  fixtureData.controller.stop();
  assert.equal(fixtureData.cleared.length, 2);
  assert.deepEqual(fixtureData.cleared, fixtureData.intervals);
});
