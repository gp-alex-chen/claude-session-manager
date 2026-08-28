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

function renderDetails(documentRef, details, summary, stale, error) {
  details.replaceChildren();
  if (!summary) {
    addText(documentRef, details, 'p', 'usage-details-empty', error ? '用量暂不可用' : '当前会话暂无用量');
    return;
  }
  const project = usageTotal(summary, 'project');
  const session = usageTotal(summary, 'session');
  const latest = summary.session_found && summary.latest ? usageNumbers(summary.latest) : null;

  const title = documentRef.createElement('div');
  title.className = 'usage-details-title';
  addText(documentRef, title, 'span', 'usage-details-title-text', 'Token 用量');
  if (stale) addText(documentRef, title, 'span', 'usage-details-stale', '数据较旧');
  if (error && !stale) addText(documentRef, title, 'span', 'usage-details-stale', '更新失败');
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
  const projectRequests = summary.project_found ? String(summary.project_request_count ?? 0) : '—';
  const sessionRequests = summary.session_found ? String(summary.session_request_count ?? 0) : '—';
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

function renderSummary(documentRef, button, token, entry) {
  button.replaceChildren();
  const summary = entry?.summary;
  const session = usageTotal(summary, 'session');
  let label = '用量 —';
  let disabled = true;
  if (token && entry?.waiting) {
    label = '等待统计';
  } else if (token && entry?.loading && !summary) {
    label = '加载中…';
  } else if (token && entry?.error && !summary) {
    label = '用量失败';
  } else if (token && summary) {
    label = `会话 ${session ? formatTokenCount(session.total) : '—'}`;
    if (entry.stale) label += ' · 较旧';
    disabled = false;
  } else if (token) {
    label = '暂无用量';
  }
  addText(documentRef, button, 'span', 'usage-pane-label', label);
  button.disabled = disabled;
  button.setAttribute?.('aria-busy', String(Boolean(entry?.loading)));
  return { summary, disabled };
}

export function createUsageView({
  surfaces = [],
  documentRef = typeof document === 'undefined' ? null : document,
  windowRef = typeof window === 'undefined' ? null : window,
}) {
  const surfaceList = typeof surfaces === 'function' ? surfaces() : surfaces;
  const surfaceById = new Map(surfaceList.map((surface) => [surface.id, surface]));
  let openPaneId = null;
  let openToken = null;
  const tokenByPane = new Map();
  let started = false;
  const clickHandlers = new Map();

  function setOpen(paneId, next) {
    const surface = surfaceById.get(paneId);
    if (!surface) return;
    if (next && surface.usageSummary.disabled) return;
    if (openPaneId && openPaneId !== paneId) {
      const previous = surfaceById.get(openPaneId);
      if (previous) {
        previous.usageDetails.hidden = true;
        previous.usageDetails.setAttribute?.('aria-hidden', 'true');
        previous.usageSummary.setAttribute?.('aria-expanded', 'false');
      }
    }
    const open = Boolean(next);
    surface.usageDetails.hidden = !open;
    surface.usageDetails.setAttribute?.('aria-hidden', String(!open));
    surface.usageSummary.setAttribute?.('aria-expanded', String(open));
    openPaneId = open ? paneId : null;
    openToken = open ? tokenByPane.get(paneId) || null : null;
  }

  function toggle(paneId, event) {
    event?.stopPropagation?.();
    setOpen(paneId, openPaneId !== paneId);
  }

  function onKeyDown(event) {
    if (event.key === 'Escape' && openPaneId) setOpen(openPaneId, false);
  }

  function onDocumentClick(event) {
    if (!openPaneId) return;
    const surface = surfaceById.get(openPaneId);
    if (!surface) return;
    if (
      event.target === surface.usageSummary
      || surface.usageSummary.contains?.(event.target)
      || surface.usageDetails.contains?.(event.target)
    ) return;
    setOpen(openPaneId, false);
  }

  function render(state) {
    const panes = new Map((state.panes || []).map((pane) => [pane.id, pane]));
    for (const surface of surfaceList) {
      const pane = panes.get(surface.id);
      const token = pane?.token || null;
      tokenByPane.set(surface.id, token);
      const entry = token ? state.usageByToken?.get(token) : null;
      const result = renderSummary(documentRef, surface.usageSummary, token, entry);
      surface.usageSummary.setAttribute?.('aria-controls', surface.usageDetails.id);
      const paneNumber = Number(surface.id.slice(-1)) + 1;
      surface.usageSummary.setAttribute?.('aria-label', token
        ? '查看窗格 ' + paneNumber + ' token 用量'
        : '窗格 ' + paneNumber + ' 当前没有会话');
      if (openPaneId === surface.id && (result.disabled || openToken !== token)) {
        setOpen(surface.id, false);
      }
      renderDetails(documentRef, surface.usageDetails, result.summary, entry?.stale, entry?.error);
      if (openPaneId !== surface.id) {
        surface.usageDetails.hidden = true;
        surface.usageDetails.setAttribute?.('aria-hidden', 'true');
      }
    }
  }

  function start() {
    if (started) return;
    started = true;
    for (const surface of surfaceList) {
      const handler = (event) => toggle(surface.id, event);
      clickHandlers.set(surface.id, handler);
      surface.usageSummary.addEventListener?.('click', handler);
    }
    windowRef?.addEventListener?.('keydown', onKeyDown);
    documentRef?.addEventListener?.('click', onDocumentClick);
  }

  function stop() {
    if (!started) return;
    started = false;
    for (const surface of surfaceList) {
      surface.usageSummary.removeEventListener?.('click', clickHandlers.get(surface.id));
    }
    clickHandlers.clear();
    windowRef?.removeEventListener?.('keydown', onKeyDown);
    documentRef?.removeEventListener?.('click', onDocumentClick);
    if (openPaneId) setOpen(openPaneId, false);
  }

  for (const surface of surfaceList) {
    surface.usageSummary.setAttribute?.('aria-expanded', 'false');
    surface.usageDetails.hidden = true;
    surface.usageDetails.setAttribute?.('aria-hidden', 'true');
  }
  return { render, start, stop };
}
