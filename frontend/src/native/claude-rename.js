import { renameSession } from '@anthropic-ai/claude-agent-sdk/core';

// This helper calls the official offline mutation API; it never creates a query.
async function main() {
  const { sessionId, title, dir } = JSON.parse(process.argv[2]);
  await renameSession(sessionId, title, { dir });
  process.stdout.write(JSON.stringify({ sessionId, title, renamed: true }) + '\n');
}

main().catch((error) => {
  process.stderr.write(String(error.message || error) + '\n');
  process.exitCode = 1;
});
