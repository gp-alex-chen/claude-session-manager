import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';

test('bundled official SDK rename only appends the title and survives a fresh process', () => {
  const root = mkdtempSync(join(tmpdir(), 'native-rename-'));
  try {
    const home = join(root, 'home');
    const dir = join(root, 'project');
    mkdirSync(dir, { recursive: true });
    const project = join(home, 'projects', dir.replace(/[^a-zA-Z0-9]/g, '-'));
    mkdirSync(project, { recursive: true });
    const sessionId = '01900000-0000-7000-8000-000000000001';
    const file = join(project, `${sessionId}.jsonl`);
    const records = [
      { type: 'user', sessionId, cwd: dir, uuid: 'user-1', message: { role: 'user', content: 'Synthetic fixture only' } },
      { type: 'custom-title', sessionId, customTitle: 'Old title' },
      { type: 'mode', sessionId, mode: 'plan' },
      { type: 'cost', sessionId, totalCostUSD: 12.5, totalDurationMs: 1234, modelUsage: { synthetic: { inputTokens: 123, outputTokens: 456 } } },
    ];
    const before = records.map(record => JSON.stringify(record) + '\n').join('');
    writeFileSync(file, before);
    const helper = readFileSync(resolve('../internal/provider/claude_rename_helper.cjs'));
    for (const title of ['中文 "引号" \\ 路径 & $(不会执行)', 'Second native title']) {
      const result = spawnSync(process.execPath, ['-', JSON.stringify({ sessionId, title, dir })], {
        input: helper, cwd: root, encoding: 'utf8', timeout: 15000,
        env: { ...process.env, CLAUDE_CONFIG_DIR: home }, windowsHide: true,
      });
      assert.equal(result.status, 0, result.stderr);
      assert.deepEqual(JSON.parse(result.stdout), { sessionId, title, renamed: true });
    }
    const after = readFileSync(file, 'utf8');
    assert.ok(after.startsWith(before), 'existing transcript must remain byte-for-byte unchanged');
    const additions = after.slice(before.length).trim().split('\n').map(JSON.parse);
    assert.deepEqual(additions.map(record => record.type), ['custom-title', 'custom-title']);
    assert.equal(additions.at(-1).customTitle, 'Second native title');
    assert.ok(additions.every(record => record.sessionId === sessionId));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
