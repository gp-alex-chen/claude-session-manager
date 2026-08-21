import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const frontendDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const binding = fs.readFileSync(path.join(frontendDir, 'wailsjs/go/app/App.js'), 'utf8');
const backend = fs.readFileSync(path.join(frontendDir, 'src/api/backend.js'), 'utf8');
const expected = [
  'GetAgents', 'GetOpenSessions', 'GetShell', 'ShellInstalled', 'SetShell',
  'NotifyBeep', 'DebugLog', 'ListSessions', 'ListHiddenSessions', 'RenameSession',
  'DeleteSession', 'UnhideSession', 'StartSession', 'StartNew', 'TermWrite',
  'TermResize', 'TermKill', 'GetVersion', 'CheckForUpdate', 'UpdateToLatest',
];

const wrapperNames = [...binding.matchAll(/export function (\w+)\s*\(/g)].map((match) => match[1]);
const backendNames = [...backend.matchAll(/\b(\w+),?/g)]
  .map((match) => match[1])
  .filter((name) => expected.includes(name));

test('Wails wrapper and backend boundary expose the same 20 methods', () => {
  assert.deepEqual(new Set(wrapperNames), new Set(expected));
  assert.deepEqual(new Set(backendNames), new Set(expected));
  assert.equal(wrapperNames.length, expected.length);
});

test('every wrapper forwards to the matching Wails App method', () => {
  for (const name of expected) {
    const pattern = new RegExp(
      `export function ${name}\\s*\\([^)]*\\)\\s*\\{[\\s\\S]*?window\\['go'\\]\\['app'\\]\\['App'\\]\\['${name}'\\]\\(`,
    );
    assert.match(binding, pattern, name);
  }
});
