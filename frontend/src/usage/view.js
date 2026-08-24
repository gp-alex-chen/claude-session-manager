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

function detailRow(documentRef, parent, label, value) {
  const row = documentRef.createElement('div');
  row.className = 'usage-detail-row';
  addText(documentRef, row, 'span', 'usage-detail-label', label);
  addText(documentRef, row, 'span', 'usage-detail-value', value);
  parent.appendChild(row);
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
  addText(documentRef, button, 'span', 'usage-project-chip', `项目 ${formatTokenCount(project.total)}`);
  addText(documentRef, button, 'span', 'usage-cache-chip', `缓存 ${cache}`);
  addText(documentRef, button, 'span', 'usage-session-chip', `会话 ${session ? formatTokenCount(session.total) : '—'}`);
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
  title.textContent = state.usageStale ? '用量详情 · 数据较旧' : '用量详情';
  details.appendChild(title);

  const projectSection = documentRef.createElement('section');
  projectSection.className = 'usage-details-section';
  addText(documentRef, projectSection, 'h3', 'usage-details-heading', '项目累计');
  detailRow(documentRef, projectSection, '总 token', project ? formatTokenCount(project.total) : '—');
  detailRow(documentRef, projectSection, '请求数', summary?.project_found ? String(summary.project_request_count ?? 0) : '—');
  detailRow(documentRef, projectSection, '缓存命中率', project ? formatPercent(project.cacheHitRate) : '—');
  details.appendChild(projectSection);

  const sessionSection = documentRef.createElement('section');
  sessionSection.className = 'usage-details-section';
  addText(documentRef, sessionSection, 'h3', 'usage-details-heading', '当前会话');
  detailRow(documentRef, sessionSection, '累计 token', session ? formatTokenCount(session.total) : '—');
  detailRow(documentRef, sessionSection, '请求数', summary?.session_found ? String(summary.session_request_count ?? 0) : '—');
  details.appendChild(sessionSection);

  const requestSection = documentRef.createElement('section');
  requestSection.className = 'usage-details-section';
  addText(documentRef, requestSection, 'h3', 'usage-details-heading', '本轮请求');
  detailRow(documentRef, requestSection, '总 token', valueOrDash(latest?.total));
  detailRow(documentRef, requestSection, '新输入', valueOrDash(latest?.input));
  detailRow(documentRef, requestSection, '输出', valueOrDash(latest?.output));
  detailRow(documentRef, requestSection, '缓存读取', valueOrDash(latest?.cacheRead));
  detailRow(documentRef, requestSection, '缓存写入', valueOrDash(latest?.cacheCreation));
  detailRow(documentRef, requestSection, '缓存写入 5m', valueOrDash(latest?.cache5m));
  detailRow(documentRef, requestSection, '缓存写入 1h', valueOrDash(latest?.cache1h));
  detailRow(documentRef, requestSection, 'Thinking（输出明细）', valueOrDash(latest?.thinking));
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
