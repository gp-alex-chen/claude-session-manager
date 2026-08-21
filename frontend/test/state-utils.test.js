import test from 'node:test';
import assert from 'node:assert/strict';

import { createAppState } from '../src/state/app-state.js';
import { b64ToBytes, bytesToB64, clampProgress, leafOf } from '../src/utils.js';
import { createTermOptions, THEMES } from '../src/themes/catalog.js';

test('createAppState returns independent containers', () => {
  const first = createAppState();
  const second = createAppState();

  first.terminals.set('one', { token: 'one' });
  first.pendingNew.push({ token: 'one', dir: 'work' });
  first.activeToken = 'one';

  assert.equal(second.terminals.size, 0);
  assert.deepEqual(second.pendingNew, []);
  assert.equal(second.activeToken, null);
});

test('shared state is the single source for primitive and array updates', () => {
  const state = createAppState();
  state.activeToken = 'token';
  state.pendingNew.push({ token: 'token', dir: 'work' });

  assert.equal(state.activeToken, 'token');
  assert.equal(state.pendingNew[0].token, 'token');
});

test('base64 utilities round-trip UTF-8 bytes', () => {
  const bytes = new TextEncoder().encode('中文 · Claude');
  assert.deepEqual(b64ToBytes(bytesToB64(bytes)), bytes);
});

test('leafOf handles Windows, Unix, and empty paths', () => {
  assert.equal(leafOf('C:\\work\\project'), 'project');
  assert.equal(leafOf('/var/tmp/project'), 'project');
  assert.equal(leafOf(''), '');
});

test('clampProgress handles numeric boundaries and invalid values', () => {
  assert.equal(clampProgress(-10), 0);
  assert.equal(clampProgress(42), 42);
  assert.equal(clampProgress(140), 100);
  assert.equal(clampProgress('not-a-number'), 0);
});

test('theme catalog contains all themes and default terminal options', () => {
  assert.deepEqual(Object.keys(THEMES).sort(), [
    'claude', 'dracula', 'githublight', 'nord', 'onedark',
    'onelight', 'solarized', 'solarizedlight',
  ]);
  const options = createTermOptions();
  assert.equal(options.theme, THEMES.claude);
  assert.equal(options.fontSize, 14);
  assert.equal(Object.isFrozen(THEMES.claude), true);
});
