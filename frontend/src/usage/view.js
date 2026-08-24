import {
  formatPercent,
  formatTokenCount,
  usageNumbers,
  usageTotal,
} from './format.js';

function addText(documentRef, parent, tag, className, text) {
  const element = documentRef.createElement(tag);
  if (className) element.className = className;
  element.textContent = text;
  parent.appendChild(element);
  return element;
}

function valueOrDash(value) {
  return value == null ? '—' : formatTokenCount(value);
}

function addMetric(documentRef, parent, label, value, className = '') {
  const metric = documentRef.createElement('div');
  metric.className = className ? `usage-metric ${className}` : 'usage-metric';
  addText(documentRef, metric, 'span', 'usage-metric-label', label);
  addText(documentRef, metric, 'strong', 'usage-metric-value', value);
  parent.appendChild(metric);
  return metric;
}

function summaryChips(documentRef, button, state) {
  button.replaceChildren();
  const project = usageTotal(state.usageSummary, 'project');
  const session = usageTotal(state.usageSummary, 'session');
  const cache = project ? formatPercent(project.cacheHitRate) : '—';
  if (state.usageLoading && !project) {
    addText(documentRef, button, 'span', 'usage-loading-chip', '用量加载中…');
    return;
  }
  if (!project) {
    addText(documentRef, button, 'span', 'usage-unavailable-chip', '用量不可用');
    return;
  }
  addText(documentRef, button, 'span', 'usage-summary-segment usage-project-chip', `项目 ${formatTokenCount(project.total)}`);
  addText(documentRef, button, 'span', 'usage-summary-segment usage-cache-chip', `缓存 ${cache}`);
  addText(documentRef, button, 'span', 'usage-summary-segment usage-session-chip', `会话 ${session ? formatTokenCount(session.total) : '—'}`);
  if (state.usageLoading) addText(documentRef, button, 'span', 'usage-loading-chip', '更新中…');
  if (state.usageStale) addText(documentRef, button, 'span', 'usage-stale-chip', '数据较旧');
}

function renderDetails(documentRef, details, state) {
  details.replaceChildren();
  const summary = state.usageSummary;
  const project = usageTotal(summary, 'project');
  const session = usageTotal(summary, 'session');
  const latest = summary?.session_found && summary?.latest ? usageNumbers(summary.latest) : null;

  const title = documentRef.createElement('div');
  title.className = 'usage-details-title';
  addText(documentRef, title, 'span', 'usage-details-title-text', 'Token 用量');
  if (state.usageStale) addText(documentRef, title, 'span', 'usage-details-stale', '数据较旧');
  details.appendChild(title);

  const hero = documentRef.createElement('div');
  hero.className = 'usage-hero-grid';
  addMetric(documentRef, hero, '项目总量', project ? formatTokenCount(project.total) : '—', 'usage-hero-metric');
  addMetric(documentRef, hero, '当前会话', session ? formatTokenCount(session.total) : '—', 'usage-hero-metric');
  addMetric(documentRef, hero, '缓存命中', project ? formatPercent(project.cacheHitRate) : '—', 'usage-hero-metric');
  details.appendChild(hero);

  const compare = documentRef.createElement('table');
  compare.className = 'usage-compare-table';
  const head = documentRef.createElement('thead');
  const headRow = documentRef.createElement('tr');
  addText(documentRef, headRow, 'th', 'usage-compare-label', '累计指标');
  addText(documentRef, headRow, 'th', '', '项目');
  addText(documentRef, headRow, 'th', '', '会话');
  head.appendChild(headRow);
  compare.appendChild(head);
  const body = documentRef.createElement('tbody');
  const projectRequests = summary?.project_found ? String(summary.project_request_count ?? 0) : '—';
  const sessionRequests = summary?.session_found ? String(summary.session_request_count ?? 0) : '—';
  const rows = [
    ['请求数', projectRequests, sessionRequests],
    ['新输入', valueOrDash(project?.input), valueOrDash(session?.input)],
    ['输出', valueOrDash(project?.output), valueOrDash(session?.output)],
    ['缓存读取', valueOrDash(project?.cacheRead), valueOrDash(session?.cacheRead)],
    ['缓存写入', valueOrDash(project?.cacheCreation), valueOrDash(session?.cacheCreation)],
    ['思考 Token', valueOrDash(project?.thinking), valueOrDash(session?.thinking)],
  ];
  for (const [label, projectValue, sessionValue] of rows) {
    const row = documentRef.createElement('tr');
    addText(documentRef, row, 'th', 'usage-compare-label', label);
    addText(documentRef, row, 'td', 'usage-compare-value', projectValue);
    addText(documentRef, row, 'td', 'usage-compare-value', sessionValue);
    body.appendChild(row);
  }
  compare.appendChild(body);
  details.appendChild(compare);
  addText(documentRef, details, 'p', 'usage-details-note', '思考 Token 已包含在输出中');

  const requestSection = documentRef.createElement('section');
  requestSection.className = 'usage-request-section';
  addText(documentRef, requestSection, 'h3', 'usage-details-heading', '本轮请求');
  const requestGrid = documentRef.createElement('div');
  requestGrid.className = 'usage-request-grid';
  for (const [label, value] of [
    ['总量', valueOrDash(latest?.total)],
    ['新输入', valueOrDash(latest?.input)],
    ['输出', valueOrDash(latest?.output)],
    ['缓存读取', valueOrDash(latest?.cacheRead)],
    ['缓存写入', valueOrDash(latest?.cacheCreation)],
    ['思考 Token', valueOrDash(latest?.thinking)],
  ]) addMetric(documentRef, requestGrid, label, value);
  requestSection.appendChild(requestGrid);
  const tiers = `缓存层级 · 5m ${valueOrDash(latest?.cache5m)} · 1h ${valueOrDash(latest?.cache1h)}`;
  addText(documentRef, requestSection, 'p', 'usage-request-tiers', tiers);
  details.appendChild(requestSection);
}

export function createUsageView({
  summaryButton,
  details,
  documentRef = typeof document === 'undefined' ? null : document,
  windowRef = typeof window === 'undefined' ? null : window,
}) {
  let open = false;
  let started = false;

  function setOpen(next) {
    open = Boolean(next);
    details.hidden = !open;
    summaryButton.setAttribute('aria-expanded', String(open));
  }

  function toggle(event) {
    event?.stopPropagation?.();
    setOpen(!open);
  }

  function onKeyDown(event) {
    if (event.key === 'Escape' && open) setOpen(false);
  }

  function onDocumentClick(event) {
    if (!open) return;
    if (event.target === summaryButton || details.contains?.(event.target)) return;
    setOpen(false);
  }

  function start() {
    if (started) return;
    started = true;
    summaryButton.addEventListener('click', toggle);
    windowRef?.addEventListener?.('keydown', onKeyDown);
    documentRef?.addEventListener?.('click', onDocumentClick);
  }

  function stop() {
    if (!started) return;
    started = false;
    summaryButton.removeEventListener?.('click', toggle);
    windowRef?.removeEventListener?.('keydown', onKeyDown);
    documentRef?.removeEventListener?.('click', onDocumentClick);
    setOpen(false);
  }

  function render(state) {
    summaryButton.type = 'button';
    summaryButton.setAttribute('aria-controls', details.id || 'usage-details');
    summaryButton.setAttribute('aria-expanded', String(open));
    summaryButton.setAttribute('aria-label', '查看 token 用量详情');
    summaryChips(documentRef, summaryButton, state);
    renderDetails(documentRef, details, state);
    details.hidden = !open;
  }

  setOpen(false);
  return { render, start, stop };
}
