import { query } from '@anthropic-ai/claude-agent-sdk';
import { spawn } from 'node:child_process';
import { readClaudeQuota } from './claude-quota-query.js';

async function main() {
  const { executable, cwd } = JSON.parse(process.argv[2]);
  const abortController = new AbortController();
  const timer = setTimeout(() => abortController.abort(), 15_000);
  process.stdin.resume();
  process.stdin.on('end', () => abortController.abort());
  try {
    // Go releases this gate only after the helper joins its Windows job.
    await new Promise((resolve, reject) => {
      process.stdin.once('data', resolve);
      abortController.signal.addEventListener('abort', () => reject(new Error('cancelled')), { once: true });
    });
    const result = await readClaudeQuota(query, executable, cwd, abortController,
      ({ command, args, cwd: childCwd, env, signal }) => {
        const child = spawn(command, args, { cwd: childCwd, env, signal,
          windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] });
        child.stderr.resume();
        return child;
      });
    process.stdout.write(JSON.stringify(result) + '\n');
  } finally {
    clearTimeout(timer);
    process.stdin.pause();
  }
}
main().catch(() => { process.stderr.write('Claude quota control unavailable\n'); process.exitCode = 1; });
