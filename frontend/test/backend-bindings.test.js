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

function extractBackendExports(source) {
  const block = source.match(/export\s*\{([\s\S]*?)\}\s*from/);
  assert.ok(block, 'backend export block must exist');
  return block[1]
    .split(',')
    .map((entry) => entry.trim())
    .filter(Boolean)
    .map((entry) => entry.split(/\s+as\s+/).at(-1));
}

const backendNames = extractBackendExports(backend);

test('Wails wrapper and backend boundary expose the same 20 methods', () => {
  assert.equal(new Set(backendNames).size, backendNames.length);
  assert.deepEqual(new Set(wrapperNames), new Set(expected));
  assert.deepEqual(new Set(backendNames), new Set(expected));
  assert.equal(wrapperNames.length, expected.length);
});

test('binding export parser does not discard unexpected names', () => {
  const fake = backend.replace('GetAgents,', 'GetAgents, Unexpected,');
  assert.ok(extractBackendExports(fake).includes('Unexpected'));
});

test('every wrapper forwards to the matching Wails App method', () => {
  for (const name of expected) {
    const pattern = new RegExp(
      `export function ${name}\\s*\\([^)]*\\)\\s*\\{[\\s\\S]*?window\\['go'\\]\\['app'\\]\\['App'\\]\\['${name}'\\]\\(`,
    );
    assert.match(binding, pattern, name);
  }
});
