// The input stream deliberately never supplies a user message: this reads a
// control endpoint only, without invoking a model or resuming a transcript.
export async function readClaudeQuota(queryFn, executable, cwd, abortController, spawnClaudeCodeProcess) {
  async function* emptyPrompt() {
    if (!abortController.signal.aborted) await new Promise((resolve) => {
      abortController.signal.addEventListener('abort', resolve, { once: true });
    });
  }
  const session = queryFn({ prompt: emptyPrompt(), options: {
    pathToClaudeCodeExecutable: executable, cwd, abortController,
    persistSession: false, settingSources: [], tools: [], mcpServers: {},
    // --bare skips OAuth and would hide subscription allowances. Keep normal
    // CLI authentication while explicitly disabling hooks and project settings.
    strictMcpConfig: true, settings: { disableAllHooks: true }, spawnClaudeCodeProcess,
  } });
  try {
    // accountInfo is initialization metadata, not a model or quota request.
    // Missing metadata must not be treated as proof of API authentication.
    const account = typeof session.accountInfo === 'function' ? await session.accountInfo() : null;
    const apiSources = ['ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'apiKeyHelper', '/login managed key',
      'user', 'project', 'org', 'temporary'];
    const apiProviders = ['bedrock', 'vertex', 'foundry', 'anthropicAws', 'anthropicGoogleCloud', 'mantle', 'gateway'];
    const api = account && (apiProviders.includes(account.apiProvider)
      || (!account.subscriptionType && apiSources.includes(account.apiKeySource)));
    if (api) return { auth_mode: 'api', subscription_type: null, rate_limits_available: false, rate_limits: null };
    const read = session.usage_EXPERIMENTAL_MAY_CHANGE_DO_NOT_RELY_ON_THIS_API_YET;
    if (typeof read !== 'function') throw new Error('quota control unavailable');
    const result = await read.call(session, { skipBehaviors: true });
    // Exclude transcript costs, behavior scans, and any account identifiers.
    return { auth_mode: result.subscription_type ? 'subscription' : 'unknown', subscription_type: result.subscription_type,
      rate_limits_available: result.rate_limits_available, rate_limits: result.rate_limits };
  } finally {
    abortController.abort();
    if (typeof session.return === 'function') await session.return();
    else session.close();
  }
}
