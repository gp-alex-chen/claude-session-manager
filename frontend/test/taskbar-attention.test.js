import test from 'node:test';
import assert from 'node:assert/strict';
import { createAppState } from '../src/state/app-state.js';
import { createAgentController } from '../src/agents/controller.js';
import { createCompletionController } from '../src/notifications/controller.js';
import { createTerminalController } from '../src/terminal/controller.js';

const flush = async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); };

function fixture(update) {
  const state = createAppState();
  const writes = [];
  const listeners = new Map();
  const doc = { visibilityState: 'visible', hasFocus: () => true,
    addEventListener: (name, callback) => listeners.set(name, callback),
    removeEventListener: name => listeners.delete(name) };
  let completion;
  const agents = createAgentController({ state, listRoot: null, documentRef: null,
    GetAgents: async () => [], DebugLog() {}, setStatus() {},
    onTaskCompleted: (...args) => completion.complete(...args),
    onUnreadChange: () => completion?.sync(),
    setTimeoutFn: () => 0, clearTimeoutFn() {} });
  completion = createCompletionController({ state, documentRef: doc, windowRef: doc,
    getAttention: () => agents.hasTaskbarAttention(),
    SetTaskbarStatus: async (...args) => { writes.push(args); await update?.(...args); } });
  completion.start();
  const add = (id, provider = 'claude', runtimeToken = id) => {
    state.agentProviders.set(provider, { hasStatus: true });
    state.sessionProviders.set(id, provider);
    state.sessionProviders.set(runtimeToken, provider);
    state.terminals.set(runtimeToken, { exited: false });
    if (runtimeToken !== id) {
      state.newToReal.set(runtimeToken, id);
      state.realToNew.set(id, runtimeToken);
    }
  };
  return { state, writes, listeners, agents, completion, add };
}

test('approval and input waits produce taskbar attention; other states do not', async () => {
  for (const provider of ['claude', 'codex']) {
    const f = fixture();
    f.add('session', provider);
    for (const [fields, expected] of [
      [{ state: 'blocked', waitingFor: 'approval' }, true],
      [{ state: 'blocked', waitingFor: 'userInput' }, true],
      [{ state: 'idle', status: 'waiting' }, true],
      [{ state: 'working', status: 'waiting' }, false],
      [{ state: 'queued' }, false],
      [{ state: 'failed' }, false],
      [{ state: 'interrupted' }, false],
      [{ state: 'unknown' }, false],
      [{ state: 'done' }, false],
    ]) {
      f.agents.applyAgents([{ sessionId: 'session', provider, kind: 'interactive', ...fields }]);
      await flush();
      assert.equal(f.writes.at(-1)?.[1], expected, provider + ' ' + fields.state);
    }
    f.agents.applyAgents([]);
    await flush();
    assert.equal(f.writes.at(-1)[1], false);
    f.completion.stop();
  }
});

test('attention remains while focused and restores the completion count after all waits clear', async () => {
  const f = fixture();
  f.add('a'); f.add('b', 'codex');
  const blocked = id => ({ sessionId: id, provider: id === 'b' ? 'codex' : 'claude', state: 'blocked' });
  f.state.activeToken = 'a';
  f.agents.applyAgents([blocked('a'), blocked('b')]);
  f.completion.complete('finished');
  f.completion.activate('a');
  f.listeners.get('focus')();
  await flush();
  assert.deepEqual(f.writes.at(-1).slice(0, 2), [1, true]);
  f.agents.applyAgents([blocked('b')]);
  await flush();
  assert.deepEqual(f.writes.at(-1).slice(0, 2), [1, true]);
  f.agents.applyAgents([]);
  await flush();
  assert.deepEqual(f.writes.at(-1).slice(0, 2), [1, false]);
  f.completion.stop();
});

test('aggregate stays active until the last wait and last unread completion are both cleared', async () => {
  const f = fixture(); f.add('input'); f.add('approval', 'codex');
  const waiting = [
    { sessionId: 'input', state: 'blocked', waitingFor: 'userInput' },
    { sessionId: 'approval', provider: 'codex', state: 'blocked', waitingFor: 'approval' },
  ];
  const active = () => { const [count, waiting] = f.writes.at(-1); return count > 0 || waiting; };
  f.agents.applyAgents(waiting); f.completion.complete('done-a'); f.completion.complete('done-b');
  await flush(); assert.equal(active(), true);
  f.agents.applyAgents(waiting.slice(1)); f.completion.dismiss('done-a');
  await flush(); assert.equal(active(), true);
  f.state.activeToken = 'done-b'; f.completion.activate('done-b');
  await flush(); assert.deepEqual(f.writes.at(-1).slice(0, 2), [0, true]);
  f.agents.applyAgents([]); await flush(); assert.equal(active(), false);
  f.completion.stop();
});

test('closing a mapped Codex runtime suppresses stale blocked snapshots and reopening restores attention', async () => {
  const f = fixture();
  f.add('codex:saved', 'codex', 'new-codex-one');
  const blocked = { sessionId: 'codex:saved', runtimeToken: 'new-codex-one', provider: 'codex', state: 'blocked' };
  f.agents.applyAgents([blocked]);
  await flush();
  assert.equal(f.writes.at(-1)[1], true);
  f.state.closedTokens.add('new-codex-one');
  f.completion.dismiss('new-codex-one');
  f.state.newToReal.clear(); f.state.realToNew.clear();
  f.agents.applyAgents([blocked]);
  await flush();
  assert.equal(f.writes.at(-1)[1], false);
  f.state.closedTokens.clear();
  f.add('codex:saved', 'codex', 'new-codex-one');
  f.agents.applyAgents([blocked]);
  await flush();
  assert.equal(f.writes.at(-1)[1], true);
  f.state.terminals.get('new-codex-one').exited = true;
  f.completion.sync();
  await flush();
  assert.equal(f.writes.at(-1)[1], false);
  f.completion.stop();
});

test('pending taskbar writes serialize the combined state and stop clears an active warning', async () => {
  let release;
  const delayed = new Promise(resolve => { release = resolve; });
  let inFlight = 0; let maximum = 0;
  const f = fixture(async () => { maximum = Math.max(maximum, ++inFlight); await delayed; inFlight--; });
  f.add('waiting');
  f.agents.applyAgents([{ sessionId: 'waiting', state: 'blocked' }]);
  f.completion.complete('finished');
  f.completion.stop();
  release(); await flush();
  assert.equal(maximum, 1);
  assert.deepEqual(f.writes.at(-1), [0, false, true]);
});

test('closing a waiting runtime marks it closed before syncing and preserves identity until dismissal', async () => {
  for (const missingTerminal of [false, true]) {
    const f = fixture();
    f.add('codex:saved', 'codex', 'new-codex-close');
    f.state.terminals.get('new-codex-close').host = { remove() {} };
    f.agents.applyAgents([{ sessionId: 'codex:saved', runtimeToken: 'new-codex-close', provider: 'codex', state: 'blocked' }]);
    f.completion.complete('finished');
    await flush();
    assert.deepEqual(f.writes.at(-1).slice(0, 2), [1, true]);
    if (missingTerminal) f.state.terminals.delete('new-codex-close');
    let beforeClose = 0;
    const terminal = createTerminalController({ state: f.state, backend: { TermKill() {} },
      onBeforeClose: token => {
        beforeClose++;
        assert.equal(f.state.closedTokens.has(token), true);
        assert.equal(f.state.newToReal.get(token), 'codex:saved');
        f.completion.dismiss(token);
      },
      onDispose: f.completion.dismiss,
    });
    terminal.closeTab('new-codex-close');
    await flush();
    assert.equal(beforeClose, 1);
    assert.deepEqual(f.writes.at(-1).slice(0, 2), [1, false]);
    f.completion.stop();
  }
});
