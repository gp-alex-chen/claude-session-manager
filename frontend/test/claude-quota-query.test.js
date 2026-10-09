import test from 'node:test';
import assert from 'node:assert/strict';
import { readClaudeQuota } from '../src/native/claude-quota-query.js';

test('Claude quota uses control only, disables persistence and closes on success/failure', async () => {
  for (const fail of [false, true]) {
    const abort = new AbortController();
    let input; let closed = false; let options;
    const run = readClaudeQuota((config) => {
      options = config.options; input = config.prompt;
      return { usage_EXPERIMENTAL_MAY_CHANGE_DO_NOT_RELY_ON_THIS_API_YET: async (opts) => {
        assert.deepEqual(opts, { skipBehaviors: true });
        if (fail) throw new Error('unsupported control');
        return { subscription_type: 'max', rate_limits_available: true, rate_limits: {}, behaviors: 'private' };
      }, close: () => { closed = true; } };
    }, 'fake-cli', 'isolated', abort);
    if (fail) await assert.rejects(run, /unsupported/);
    else assert.deepEqual(await run, { auth_mode: 'subscription', subscription_type: 'max', rate_limits_available: true, rate_limits: {} });
    assert.equal(closed, true);
    assert.equal(abort.signal.aborted, true);
    assert.equal((await input.next()).done, true, 'must never send a user prompt');
    assert.equal(options.persistSession, false);
    assert.deepEqual(options.settingSources, []);
    assert.deepEqual(options.tools, []);
    assert.equal(options.strictMcpConfig, true);
    assert.equal(options.settings.disableAllHooks, true);
    assert.equal(options.extraArgs?.bare, undefined, 'bare disables subscription OAuth');
  }
});

test('confirmed Claude API auth skips usage control and never exposes account identity', async () => {
  let usageCalls=0;let closed=false;
  const result=await readClaudeQuota(()=>({accountInfo:async()=>({apiProvider:'firstParty',apiKeySource:'ANTHROPIC_API_KEY',tokenSource:'none',email:'private@example.test'}),
    usage_EXPERIMENTAL_MAY_CHANGE_DO_NOT_RELY_ON_THIS_API_YET:async()=>{usageCalls++;},close:()=>{closed=true;}}), 'fake-cli','isolated',new AbortController());
  assert.equal(result.auth_mode,'api');assert.equal(usageCalls,0);assert.equal(closed,true);assert.equal(result.email,undefined);
});

test('unknown or subscription auth does not disable quota reads', async () => {
  for (const account of [{}, {apiKeySource:'none'}, {subscriptionType:'max',apiKeySource:'ANTHROPIC_API_KEY'}, {apiProvider:'future-provider'}]) {
    let calls=0;
    const result=await readClaudeQuota(()=>({accountInfo:async()=>account,
      usage_EXPERIMENTAL_MAY_CHANGE_DO_NOT_RELY_ON_THIS_API_YET:async()=>{calls++;return {subscription_type:'max',rate_limits_available:false,rate_limits:null};},
      close:()=>{}}),'fake-cli','isolated',new AbortController());
    assert.equal(calls,1);assert.notEqual(result.auth_mode,'api');
  }
});
