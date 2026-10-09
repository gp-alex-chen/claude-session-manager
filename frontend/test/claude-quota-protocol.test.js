import test from 'node:test';
import assert from 'node:assert/strict';
import { query } from '@anthropic-ai/claude-agent-sdk';
import { spawn } from 'node:child_process';
import { readClaudeQuota } from '../src/native/claude-quota-query.js';

test('real SDK wire sends only initialize and get_usage, waits for native child exit', async () => {
  const wire = [];
  let child;
  const abort = new AbortController();
  const quota = await readClaudeQuota(query, 'fake.exe', process.cwd(), abort, ({ args }) => {
    assert.equal(args.includes('--bare'), false);
    assert.deepEqual(JSON.parse(args[args.indexOf('--settings') + 1]), { disableAllHooks: true });
    child = spawn(process.execPath, ['-e', `
      const readline = require('node:readline');
      const rl = readline.createInterface({ input: process.stdin });
      rl.on('line', line => {
        const m = JSON.parse(line);
        process.stderr.write(JSON.stringify(m) + '\\n');
        if (m.type !== 'control_request') process.exit(2);
        const response = m.request.subtype === 'get_usage'
          ? { subscription_type:'max', rate_limits_available:true, rate_limits:{five_hour:{utilization:20,resets_at:null}} }
          : { commands:[], models:[] };
        process.stdout.write(JSON.stringify({type:'control_response',response:{subtype:'success',request_id:m.request_id,response}})+'\\n');
      });
      rl.on('close', () => process.exit(0));
    `], { windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] });
    let buffer = '';
    child.stderr.on('data', (data) => {
      buffer += data;
      const lines = buffer.split('\n'); buffer = lines.pop();
      for (const line of lines) wire.push(JSON.parse(line));
    });
    return child;
  });
  assert.equal(quota.rate_limits.five_hour.utilization, 20);
  assert.deepEqual(wire.map(m => m.request.subtype), ['initialize', 'get_usage']);
  assert.equal(wire[1].request.skip_behaviors, true);
  assert.ok(child.exitCode !== null || child.signalCode !== null, 'cleanup must wait for child exit');
});

test('SDK cancellation closes a native child blocked in quota read', async () => {
  const abort = new AbortController();
  let child;
  const pending = readClaudeQuota(query, 'fake.exe', process.cwd(), abort, () => {
    child = spawn(process.execPath, ['-e', `
      const rl = require('node:readline').createInterface({input:process.stdin});
      rl.on('line', line => {
        const m=JSON.parse(line);
        if(m.request.subtype==='initialize') process.stdout.write(JSON.stringify({type:'control_response',response:{subtype:'success',request_id:m.request_id,response:{commands:[],models:[]}}})+'\\n');
        else process.stderr.write('quota-ready\\n');
      });
      rl.on('close',()=>process.exit(0));
    `], { windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] });
    child.stderr.once('data', () => abort.abort());
    return child;
  });
  await assert.rejects(pending);
  assert.ok(child.exitCode !== null || child.signalCode !== null);
});
