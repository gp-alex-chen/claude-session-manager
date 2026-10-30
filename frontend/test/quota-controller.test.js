import test from 'node:test';
import assert from 'node:assert/strict';
import { createAppState } from '../src/state/app-state.js';
import { createQuotaController, activeQuotaProvider } from '../src/quota/controller.js';
import { ringWindows, remainingPercent } from '../src/quota/view.js';

function fixture(nowFn, storage) {
  const state = createAppState();
  state.terminals.set('new-codex-1', { provider: 'codex' });
  state.terminals.set('claude-1', { provider: 'claude' });
  let tick; let timers=0; let stopped=0; const intervals=[];
  const requests=[];const rendered=[];
  const view={render:(value)=>rendered.push(value), close:()=>{}, start:()=>{}, stop:()=>{}};
  const controller=createQuotaController({state,view,nowFn,storage,GetAccountQuota:(provider,force)=>new Promise(resolve=>requests.push({provider,force,resolve})),
    setIntervalFn:(callback,ms)=>{tick=callback;intervals.push(ms);timers++;return timers;},clearIntervalFn:()=>{stopped++;}});
  return {state,controller,requests,rendered,intervals,tick:()=>tick?.(),timers:()=>timers,stopped:()=>stopped,activeTimers:()=>timers-stopped};
}
test('quota follows focused provider, deduplicates and ignores another provider late result',async()=>{
  const f=fixture();f.controller.start();assert.equal(f.requests.length,0);assert.equal(f.timers(),0);
  f.state.activeToken='new-codex-1';f.controller.sync();f.controller.sync();
  assert.equal(f.requests.length,1);assert.equal(f.requests[0].provider,'codex');
  f.state.activeToken='claude-1';f.controller.sync();
  assert.equal(f.requests[1].provider,'claude');
  f.requests[0].resolve({provider:'codex',status:'available',windows:[]});await Promise.resolve();await Promise.resolve();
  assert.equal(f.rendered.at(-1).provider,'claude');
  f.requests[1].resolve({provider:'claude',status:'available',windows:[]});await Promise.resolve();await Promise.resolve();
  f.state.activeToken=null;f.controller.sync();assert.equal(f.rendered.at(-1).provider,null);assert.equal(f.activeTimers(),0);
  const count=f.requests.length;f.tick();assert.equal(f.requests.length,count);
});
test('a failed quota read also waits fifteen minutes before automatic retry',async()=>{
  let now=0;const f=fixture(()=>now);f.state.activeToken='claude-1';f.controller.start();
  f.requests[0].resolve({provider:'claude',status:'error',windows:[]});await Promise.resolve();await Promise.resolve();
  now=20_000;f.controller.sync();assert.equal(f.requests.length,1);
  now=899_999;f.controller.sync();assert.equal(f.requests.length,1);
  now=900_000;f.controller.sync();assert.equal(f.requests.length,2);f.controller.stop();
});
test('stopped controller rejects late results even across restart',async()=>{
  const f=fixture();f.state.activeToken='claude-1';f.controller.start();f.controller.stop();f.controller.start();
  f.requests[0].resolve({provider:'claude',plan:'old',windows:[]});await Promise.resolve();await Promise.resolve();
  assert.notEqual(f.rendered.at(-1).snapshot?.plan,'old');f.controller.stop();
});
test('active terminal provider wins over filters and stale session identity',()=>{
  const s=createAppState();s.activeToken='new-codex-1';s.terminals.set(s.activeToken,{provider:'codex'});
  s.sessionProviders.set(s.activeToken,'claude');s.selectedProviders=new Set(['claude']);
  assert.equal(activeQuotaProvider(s),'codex');s.terminals.get(s.activeToken).exited=true;assert.equal(activeQuotaProvider(s),null);
});
test('ring chooses aggregate windows by duration and treats zero as a valid remaining value',()=>{
  const windows=[{id:'opus',scope:'Opus',windowMinutes:10080,usedPercent:5},{id:'week',windowMinutes:10080,usedPercent:100},{id:'session',windowMinutes:300,usedPercent:0}];
  assert.deepEqual(ringWindows(windows).map(w=>w.id),['session','week']);
  assert.equal(remainingPercent(windows[2]),100);assert.equal(remainingPercent(windows[1]),0);
  assert.equal(remainingPercent({usedPercent:null}),null);assert.equal(remainingPercent({usedPercent:NaN}),null);
  assert.deepEqual(ringWindows([{id:'other',windowMinutes:15,usedPercent:20}]).map(w=>w.id),['other']);
});

test('confirmed API mode stops polling and repeated focus; manual subscription refresh resumes it',async()=>{
  let now=0;const f=fixture(()=>now);f.state.activeToken='new-codex-1';f.controller.start();
  f.requests[0].resolve({provider:'codex',status:'unsupported',authMode:'api',windows:[]});await Promise.resolve();await Promise.resolve();
  assert.equal(f.stopped(),1,'API mode must stop its timer');
  now=600_000;f.tick();f.controller.sync();await f.controller.refresh();
  assert.equal(f.requests.length,1,'automatic refresh must stay stopped after cache TTL');
  f.state.activeToken='claude-1';f.controller.sync();assert.equal(f.requests.length,2);
  f.requests[1].resolve({provider:'claude',status:'available',windows:[]});await Promise.resolve();await Promise.resolve();
  f.state.activeToken='new-codex-1';f.controller.sync();assert.equal(f.requests.length,2);
  const manual=f.controller.refresh(true);assert.equal(f.requests[2].force,true);
  f.requests[2].resolve({provider:'codex',status:'available',authMode:'subscription',windows:[]});await manual;
  const count=f.timers();now+=900_000;f.tick();assert.equal(f.requests.length,4);assert.ok(count>=3);
  f.controller.stop();
});

test('unavailable allowance without confirmed API auth keeps automatic retry enabled',async()=>{
  let now=0;const f=fixture(()=>now);f.state.activeToken='claude-1';f.controller.start();
  f.requests[0].resolve({provider:'claude',status:'unsupported',windows:[]});await Promise.resolve();await Promise.resolve();
  assert.equal(f.activeTimers(),1);now=900_000;f.tick();assert.equal(f.requests.length,2);f.controller.stop();
});

test('both providers poll fifteen minutes after a completed read and still allow manual refresh',async()=>{
  for (const token of ['claude-1','new-codex-1']) {
    let now=0;const f=fixture(()=>now);f.state.activeToken=token;f.controller.start();
    assert.equal(f.requests.length,1);
    now=5_000;f.requests[0].resolve({provider:f.requests[0].provider,status:'available',windows:[]});await Promise.resolve();await Promise.resolve();
    assert.ok(f.intervals.every(ms=>ms===900_000),'all quota polling intervals must be fifteen minutes');
    now=904_999;f.tick();assert.equal(f.requests.length,1);
    now=905_000;f.tick();assert.equal(f.requests.length,2,'response delay must not defer auto refresh to thirty minutes');
    f.requests[1].resolve({provider:f.requests[1].provider,status:'available',windows:[]});await Promise.resolve();await Promise.resolve();
    now+=31_000;const manual=f.controller.refresh(true);assert.equal(f.requests.length,3);assert.equal(f.requests[2].force,true);
    f.requests[2].resolve({provider:f.requests[2].provider,status:'available',windows:[]});await manual;f.controller.stop();
  }
});


test('fixed quota source survives persistence and restart without a terminal', async () => {
  const { createPreferenceStorage } = await import('../src/settings/storage.js');
  const saved = { 'ui-theme': 'dark' };
  const backend = { LoadPreferences: async () => ({ ...saved }), SetPreference: async (key, value) => { saved[key] = value; } };
  const storage = createPreferenceStorage({ backend });
  await storage.initialize();
  const f = fixture(undefined, storage);
  f.state.activeToken = 'claude-1'; f.controller.start();
  f.controller.setSource('codex');
  assert.equal(f.rendered.at(-1).source, 'codex');
  assert.equal(f.rendered.at(-1).provider, 'codex');
  f.state.activeToken = null; f.controller.sync();
  assert.equal(f.rendered.at(-1).provider, 'codex');
  f.controller.stop(); await storage.flush();
  const restored = createPreferenceStorage({ backend }); await restored.initialize();
  const next = fixture(undefined, restored); next.controller.start();
  assert.equal(next.requests[0].provider, 'codex');
  assert.equal(next.rendered.at(-1).source, 'codex');
  assert.equal(saved['ui-theme'], 'dark');
  next.controller.setSource('claude');
  assert.equal(next.requests[1].provider, 'claude');
  next.controller.setSource('auto');
  assert.equal(next.rendered.at(-1).provider, null);
  next.state.activeToken = 'new-codex-1'; next.controller.sync();
  assert.equal(next.rendered.at(-1).provider, 'codex');
  next.controller.stop(); await restored.flush();
  assert.equal(saved['quota-source'], 'auto');
});

test('source switching keeps caches separate and errors do not change the selection', async () => {
  const f = fixture(); f.controller.start();
  f.controller.setSource('codex'); f.controller.setSource('claude');
  f.requests[0].resolve({ provider: 'codex', status: 'available', plan: 'Codex fixture', windows: [] });
  await Promise.resolve(); await Promise.resolve();
  assert.equal(f.rendered.at(-1).provider, 'claude');
  assert.equal(f.rendered.at(-1).snapshot, null);
  f.requests[1].resolve({ provider: 'claude', status: 'error', message: 'not logged in', windows: [] });
  await Promise.resolve(); await Promise.resolve();
  assert.equal(f.rendered.at(-1).source, 'claude');
  assert.equal(f.rendered.at(-1).snapshot.message, 'not logged in');
  f.controller.setSource('codex');
  assert.equal(f.rendered.at(-1).snapshot.plan, 'Codex fixture');
  assert.equal(f.requests.length, 2);
  f.controller.setSource('unknown');
  assert.equal(f.rendered.at(-1).source, 'codex');
  f.controller.stop();
});

test('missing or invalid saved quota sources fall back to automatic without rewriting settings', () => {
  for (const value of [null, '', 'unknown', 'CODEX']) {
    const writes = [];
    const f = fixture(undefined, { getItem: () => value, setItem: (...args) => writes.push(args) });
    f.state.activeToken = 'claude-1'; f.controller.start();
    assert.equal(f.rendered.at(-1).source, 'auto');
    assert.equal(f.requests[0].provider, 'claude');
    assert.deepEqual(writes, []); f.controller.stop();
  }
});
