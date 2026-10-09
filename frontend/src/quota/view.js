export function remainingPercent(window) {
  const used = window?.usedPercent;
  return typeof used === 'number' && Number.isFinite(used)
    ? Math.round(Math.max(0, Math.min(100, 100 - used))) : null;
}

export function ringWindows(windows = []) {
  const shared = windows.filter(w => !w.scope && remainingPercent(w) !== null);
  const five = shared.find(w => w.windowMinutes === 300);
  const week = shared.find(w => w.windowMinutes === 10080);
  if (five || week) return [five, week].filter(Boolean);
  return shared.slice(0, 2);
}

function resetText(window) {
  return window.resetsAt > 0 ? new Date(window.resetsAt * 1000).toLocaleString('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }) + ' 重置' : '重置时间未知';
}

export function createQuotaView({ root, documentRef, onRefresh }) {
  const el = (tag, className, text) => {
    const node = documentRef.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  };
  const svgEl = (tag, attributes) => {
    const node = documentRef.createElementNS?.('http://www.w3.org/2000/svg', tag) || documentRef.createElement(tag);
    for (const [key, value] of Object.entries(attributes)) node.setAttribute(key, String(value));
    return node;
  };
  const button = el('button', 'quota-ring-button'); button.type = 'button';
  button.setAttribute('aria-expanded', 'false'); button.setAttribute('aria-controls', 'account-quota-details');
  const label = el('span', 'quota-agent-label');
  const meter = el('span', 'quota-meter'); meter.setAttribute('aria-hidden', 'true');
  button.append(label, meter);
  const panel = el('section', 'quota-details'); panel.id = 'account-quota-details'; panel.hidden = true;
  panel.setAttribute('aria-label', '当前 Agent 订阅额度');
  const content = el('div');
  const refresh = el('button', 'quota-refresh', '刷新额度'); refresh.type = 'button';
  panel.append(content, refresh); root.append(button, panel); root.hidden = true;
  function close() { panel.hidden = true; button.setAttribute('aria-expanded', 'false'); }
  const toggle = () => { panel.hidden = !panel.hidden; button.setAttribute('aria-expanded', String(!panel.hidden)); };
  const outside = (event) => { if (!root.contains?.(event.target)) close(); };
  const escape = (event) => { if (event.key === 'Escape' && !panel.hidden) { close(); button.focus?.(); } };
  const refreshClick = () => onRefresh?.();
  let started = false;

  function render({ provider, snapshot, loading, error }) {
    root.hidden = !provider;
    if (!provider) { close(); return; }
    const name = provider === 'codex' ? 'Codex' : provider === 'claude' ? 'Claude' : provider;
    label.textContent = name;
    const windows = snapshot?.status === 'available' ? snapshot.windows || [] : [];
    const selected = ringWindows(windows);
    const stale = !!snapshot?.stale || !!error;
    root.classList.toggle('quota-stale', stale);
    button.setAttribute('aria-busy', String(loading));
    const chart = svgEl('svg', { viewBox: '0 0 56 56', width: 36, height: 36 });
    const descriptions = [];
    const tracks = selected.length ? selected : [null];
    tracks.forEach((window, index) => {
      const radius = index === 0 ? 25 : 20;
      const remaining = remainingPercent(window);
      const colorClass = window?.windowMinutes === 10080 ? 'quota-week' : 'quota-short';
      chart.append(svgEl('circle', { cx: 28, cy: 28, r: radius, class: 'quota-track', fill: 'none', 'stroke-width': 3 }));
      if (remaining > 0) chart.append(svgEl('circle', { cx: 28, cy: 28, r: radius, class: colorClass,
        fill: 'none', 'stroke-width': 3, pathLength: 100, 'stroke-dasharray': `${remaining} 100`,
        'stroke-linecap': 'round', transform: 'rotate(-90 28 28)' }));
      const text = svgEl('text', { x: 28, y: tracks.length === 1 ? 33 : index === 0 ? 25 : 39,
        class: remaining === null ? 'quota-unknown' : colorClass, 'text-anchor': 'middle' });
      text.textContent = remaining === null ? loading ? '…' : '—' : `${remaining}%`;
      chart.append(text);
      if (window) descriptions.push(`${window.label || '额度'}剩余 ${remaining}% · ${resetText(window)}`);
    });
    meter.replaceChildren(chart);
    let message = error || snapshot?.message || (loading ? '正在查询订阅额度' : '暂无额度数据');
    if (stale) descriptions.push(message || '显示上次读取的额度');
    const tooltip = [name + ' 订阅额度', ...descriptions];
    if (!selected.length) tooltip.push(message);
    button.title = tooltip.join('\n'); button.setAttribute('aria-label', tooltip.join('，'));
    const title = el('div', 'quota-details-title', name + (snapshot?.plan ? ' · ' + snapshot.plan : '') + ' 订阅额度');
    const rows = windows.map(w => {
      const row = el('div', 'quota-detail-row');
      row.append(el('span', '', `${w.label || '额度'} · 剩余 ${remainingPercent(w)}%`), el('small', '', resetText(w)));
      return row;
    });
    if (!windows.length || stale) rows.push(el('p', 'quota-message', message));
    if (snapshot?.readAt && !String(snapshot.readAt).startsWith('0001')) rows.push(el('small', 'quota-read-time',
      `${stale ? '上次读取' : '读取于'} ${new Date(snapshot.readAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}`));
    content.replaceChildren(title, ...rows);
    refresh.disabled = loading; refresh.textContent = loading ? '查询中…' : '刷新额度';
  }
  function start() {
    if (started) return; started = true;
    button.addEventListener('click', toggle); refresh.addEventListener('click', refreshClick);
    documentRef.addEventListener?.('pointerdown', outside); documentRef.addEventListener?.('keydown', escape);
  }
  function stop() {
    if (!started) return; started = false; close(); root.hidden = true;
    button.removeEventListener?.('click', toggle); refresh.removeEventListener?.('click', refreshClick);
    documentRef.removeEventListener?.('pointerdown', outside); documentRef.removeEventListener?.('keydown', escape);
  }
  return { render, close, start, stop };
}
