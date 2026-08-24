import test from 'node:test';
import assert from 'node:assert/strict';

import { formatPercent, formatTokenCount, usageNumbers, usageTotal } from '../src/usage/format.js';

test('formatTokenCount uses compact K/M/B units with stable boundaries', () => {
  assert.equal(formatTokenCount(0), '0');
  assert.equal(formatTokenCount(999), '999');
  assert.equal(formatTokenCount(1_000), '1K');
  assert.equal(formatTokenCount(1_234), '1.23K');
  assert.equal(formatTokenCount(1_000_000), '1M');
  assert.equal(formatTokenCount(1_280_000), '1.28M');
  assert.equal(formatTokenCount(1_000_000_000), '1B');
  assert.equal(formatTokenCount('bad'), '0');
});

test('usageNumbers computes total and cache rate without double counting thinking', () => {
  const numbers = usageNumbers({
    input_tokens: 10,
    output_tokens: 20,
    cache_creation_input_tokens: 30,
    cache_read_input_tokens: 40,
    thinking_tokens: 5,
    cache_creation_5m_input_tokens: 11,
    cache_creation_1h_input_tokens: 19,
  });
  assert.deepEqual(numbers, {
    input: 10,
    output: 20,
    cacheCreation: 30,
    cacheRead: 40,
    thinking: 5,
    cache5m: 11,
    cache1h: 19,
    prompt: 80,
    total: 100,
    cacheHitRate: 0.5,
  });
  assert.equal(formatPercent(Number.NaN), '—');
  assert.equal(usageNumbers({}).cacheHitRate, null);
});

test('usageTotal respects scope availability instead of turning missing data into zero', () => {
  assert.equal(usageTotal({ project_found: false, project_total: { output_tokens: 3 } }, 'project'), null);
  assert.deepEqual(usageTotal({ session_found: true, session_total: { output_tokens: 3 } }, 'session').total, 3);
});
