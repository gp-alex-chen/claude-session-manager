import test from 'node:test';
import assert from 'node:assert/strict';

import { createAppState } from '../src/state/app-state.js';
import {
  DEFAULT_TERMINAL_FONT_SIZE,
  MAX_TERMINAL_FONT_SIZE,
  MIN_TERMINAL_FONT_SIZE,
  TERMINAL_FONT_SIZE_STEP,
  normalizeTerminalFontSize,
} from '../src/terminal/options.js';
import { b64ToBytes, bytesToB64, clampProgress, leafOf } from '../src/utils.js';
import { createTermOptions, THEMES } from '../src/themes/catalog.js';

test('createAppState returns independent containers', () => {
  const first = createAppState();
  const second = createAppState();

  first.terminals.set('one', { token: 'one' });
  first.pendingNew.push({ token: 'one', dir: 'work' });
  first.activeToken = 'one';
  first.panes[0].token = 'one';
  first.layoutMode = 'split-cols-2';

  assert.equal(second.terminals.size, 0);
  assert.deepEqual(second.pendingNew, []);
  assert.equal(second.activeToken, null);
  assert.equal(second.panes[0].token, null);
  assert.equal(second.layoutMode, 'single');
  assert.equal(second.focusedPaneId, 'pane-0');
});

test('shared state is the single source for primitive and array updates', () => {
  const state = createAppState();
  state.activeToken = 'token';
  state.pendingNew.push({ token: 'token', dir: 'work' });

  assert.equal(state.activeToken, 'token');
  assert.equal(state.pendingNew[0].token, 'token');
});

test('terminal font-size constants and normalization define safe integer bounds', () => {
  assert.equal(DEFAULT_TERMINAL_FONT_SIZE, 14);
  assert.equal(MIN_TERMINAL_FONT_SIZE, 10);
  assert.equal(MAX_TERMINAL_FONT_SIZE, 24);
  assert.equal(TERMINAL_FONT_SIZE_STEP, 1);

  for (const value of [undefined, null, '', '   ', Number.NaN, Infinity, -Infinity, 'invalid']) {
    assert.equal(normalizeTerminalFontSize(value), DEFAULT_TERMINAL_FONT_SIZE);
  }
  assert.equal(normalizeTerminalFontSize(9), MIN_TERMINAL_FONT_SIZE);
  assert.equal(normalizeTerminalFontSize('10'), MIN_TERMINAL_FONT_SIZE);
  assert.equal(normalizeTerminalFontSize(17.4), 17);
  assert.equal(normalizeTerminalFontSize(17.5), 18);
  assert.equal(normalizeTerminalFontSize('23.6'), MAX_TERMINAL_FONT_SIZE);
  assert.equal(normalizeTerminalFontSize(25), MAX_TERMINAL_FONT_SIZE);
});

test('app state owns an independent terminal font-size primitive', () => {
  const first = createAppState();
  const second = createAppState();

  assert.equal(first.terminalFontSize, DEFAULT_TERMINAL_FONT_SIZE);
  first.terminalFontSize = 20;
  assert.equal(first.terminalFontSize, 20);
  assert.equal(second.terminalFontSize, DEFAULT_TERMINAL_FONT_SIZE);
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
  assert.equal(options.fontSize, DEFAULT_TERMINAL_FONT_SIZE);
  assert.equal(createTermOptions('dracula', 19.6).fontSize, 20);
  assert.equal(createTermOptions('dracula', 'invalid').fontSize, DEFAULT_TERMINAL_FONT_SIZE);
  assert.equal(createTermOptions('dracula', 100).fontSize, MAX_TERMINAL_FONT_SIZE);
  assert.equal(Object.isFrozen(THEMES.claude), true);
});
