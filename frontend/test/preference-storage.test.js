import test from 'node:test';
import assert from 'node:assert/strict';
import { createPreferenceStorage } from '../src/settings/storage.js';

test('JSON preferences restore across renderers and patch keys in order', async () => {
  const saved = { 'ui-theme': 'dark', 'term-theme': 'nord', 'term-font-size': '18' };
  const writes = [];
  const backend = {
    LoadPreferences: async () => ({ ...saved }),
    SetPreference: async (key, value) => { writes.push([key, value]); saved[key] = value; },
  };
  const first = createPreferenceStorage({ backend });
  first.setItem('ui-theme', 'light'); // Startup defaults cannot overwrite disk.
  await first.initialize();
  assert.equal(first.getItem('ui-theme'), 'dark');
  first.setItem('term-font-size', '20');
  first.setItem('term-font-size', '22');
  first.setItem('term-theme', 'dracula');
  await first.flush();
  assert.deepEqual(writes, [['term-font-size', '20'], ['term-font-size', '22'], ['term-theme', 'dracula']]);
  const restarted = createPreferenceStorage({ backend });
  await restarted.initialize();
  assert.equal(restarted.getItem('ui-theme'), 'dark');
  assert.equal(restarted.getItem('term-font-size'), '22');
  assert.equal(restarted.getItem('term-theme'), 'dracula');
});

test('provider filters persist through the shared JSON preference storage', async () => {
  const saved = {};
  const backend = { LoadPreferences: async () => ({ ...saved }), SetPreference: async (key, value) => { saved[key] = value; } };
  const first = createPreferenceStorage({ backend });
  await first.initialize();
  first.setItem('session-provider-filter', '["claude"]');
  await first.flush();
  const restarted = createPreferenceStorage({ backend });
  await restarted.initialize();
  assert.equal(restarted.getItem('session-provider-filter'), '["claude"]');
});

test('load failure prevents default values from overwriting unreadable JSON', async () => {
  const errors = [];
  let writes = 0;
  const storage = createPreferenceStorage({ backend: {
    LoadPreferences: async () => { throw new Error('corrupt JSON'); },
    SetPreference: async () => { writes += 1; },
  }, onError: (error) => errors.push(error.message) });
  await storage.initialize();
  storage.setItem('ui-theme', 'light');
  await storage.flush();
  assert.equal(writes, 0);
  assert.deepEqual(errors, ['corrupt JSON']);
});

test('failed writes remain retryable and do not block later preference changes', async () => {
  const errors = [];
  const saved = {};
  let fail = true;
  const storage = createPreferenceStorage({ backend: {
    LoadPreferences: async () => ({}),
    SetPreference: async (key, value) => {
      if (fail) { fail = false; throw new Error('disk unavailable'); }
      saved[key] = value;
    },
  }, onError: (error) => errors.push(error.message) });
  await storage.initialize();
  storage.setItem('ui-theme', 'dark');
  await storage.flush();
  storage.setItem('ui-theme', 'dark');
  storage.setItem('term-theme', 'nord');
  await storage.flush();
  assert.deepEqual(saved, { 'ui-theme': 'dark', 'term-theme': 'nord' });
  assert.deepEqual(errors, ['disk unavailable']);
});
