const TOKEN_UNITS = [
  [1_000_000_000, 'B'],
  [1_000_000, 'M'],
  [1_000, 'K'],
];

function tokenNumber(value) {
  const number = typeof value === 'number' ? value : Number(value);
  return Number.isFinite(number) && number >= 0 ? number : 0;
}

function trimNumber(value) {
  return value.toFixed(2).replace(/\.0+$|(?<=\.[0-9])0+$/g, '');
}

export function formatTokenCount(value) {
  const number = tokenNumber(value);
  for (const [unit, suffix] of TOKEN_UNITS) {
    if (number >= unit) return trimNumber(number / unit) + suffix;
  }
  return String(Math.round(number));
}

export function formatPercent(rate) {
  const number = Number(rate);
  if (!Number.isFinite(number) || number < 0) return '—';
  return (number * 100).toFixed(1).replace(/\.0$/, '') + '%';
}

export function usageNumbers(usage = {}) {
  const input = tokenNumber(usage.input_tokens);
  const output = tokenNumber(usage.output_tokens);
  const cacheCreation = tokenNumber(usage.cache_creation_input_tokens);
  const cacheRead = tokenNumber(usage.cache_read_input_tokens);
  const prompt = input + cacheCreation + cacheRead;
  return {
    input,
    output,
    cacheCreation,
    cacheRead,
    thinking: tokenNumber(usage.thinking_tokens),
    cache5m: tokenNumber(usage.cache_creation_5m_input_tokens),
    cache1h: tokenNumber(usage.cache_creation_1h_input_tokens),
    prompt,
    total: prompt + output,
    cacheHitRate: prompt ? cacheRead / prompt : null,
  };
}

export function usageTotal(summary, scope = 'project') {
  const found = scope === 'session' ? summary?.session_found : summary?.project_found;
  if (!found) return null;
  return usageNumbers(scope === 'session' ? summary?.session_total : summary?.project_total);
}

export function formatProjectUsage(summary) {
  const total = usageTotal(summary, 'project');
  return total ? formatTokenCount(total.total) : '';
}

export function formatProjectUsageTitle(summary) {
  const total = usageTotal(summary, 'project');
  if (!total) return '项目用量暂无统计';
  return `项目累计 ${formatTokenCount(total.total)} · 缓存命中率 ${formatPercent(total.cacheHitRate)}`;
}
