export function remainingPercent(window) {
  const used = window?.usedPercent;
  return typeof used === 'number' && Number.isFinite(used)
    ? Math.round(Math.max(0, Math.min(100, 100 - used))) : null;
}

// A steady pace leaves the same fraction of allowance as time in the window.
// Expired or incomplete reset metadata cannot provide a meaningful reference.
export function expectedRemainingPercent(window, now = Date.now()) {
  const minutes = window?.windowMinutes;
  const reset = window?.resetsAt;
  if (!Number.isFinite(minutes) || minutes <= 0 || !Number.isFinite(reset) || reset <= 0
    || !Number.isFinite(now) || reset * 1000 <= now) return null;
  return Math.min(100, (reset * 1000 - now) / (minutes * 60_000) * 100);
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

export function createQuotaView({ root, documentRef, onRefresh, onSourceChange, nowFn = () => Date.now(),
  setIntervalFn = setInterval, clearIntervalFn = clearInterval }) {
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
  panel.setAttribute('aria-label', '订阅额度');
  const sourceField = el('label', 'quota-source-field');
  const sourceSelect = el('select', 'quota-source-select');
  for (const [value, text] of [['auto', '自动跟随会话'], ['codex', 'Codex / ChatGPT'], ['claude', 'Claude']]) {
    const option = el('option', '', text); option.value = value; sourceSelect.append(option);
  }
  sourceField.append(el('span', '', '额度来源'), sourceSelect);
  const sourceNote = el('p', 'quota-source-note', '读取本机所选 CLI 当前登录账户的订阅额度。');
  const content = el('div');
  const refresh = el('button', 'quota-refresh', '刷新额度'); refresh.type = 'button';
  panel.append(sourceField, sourceNote, content, refresh); root.append(button, panel); root.hidden = true;
  function close() { panel.hidden = true; button.setAttribute('aria-expanded', 'false'); }
  const toggle = () => { panel.hidden = !panel.hidden; button.setAttribute('aria-expanded', String(!panel.hidden)); };
  const outside = (event) => { if (!root.contains?.(event.target)) close(); };
  const escape = (event) => { if (event.key === 'Escape' && !panel.hidden) { close(); button.focus?.(); } };
  const refreshClick = () => onRefresh?.();
  const sourceChange = () => onSourceChange?.(sourceSelect.value);
  let started = false;
  let paceTimer = null;
  let lastRender = null;

  function syncPaceTimer(active) {
    // Move the time reference locally; this never requests account quota.
    if (started && active && paceTimer === null) paceTimer = setIntervalFn(() => {
      if (started && lastRender) render(lastRender);
    }, 60_000);
    if ((!started || !active) && paceTimer !== null) {
      clearIntervalFn(paceTimer); paceTimer = null;
    }
  }

  function render(input) {
    lastRender = input;
    const { provider, source = 'auto', snapshot, loading, error } = input;
    sourceSelect.value = source;
    root.hidden = !provider;
    if (!provider) { syncPaceTimer(false); close(); return; }
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
    const now = nowFn();
    syncPaceTimer(selected.some(window => expectedRemainingPercent(window, now) !== null));
    tracks.forEach((window, index) => {
      const radius = index === 0 ? 25 : 20;
      const remaining = remainingPercent(window);
      const used = remaining === null ? null : 100 - remaining;
      const colorClass = window?.windowMinutes === 10080 ? 'quota-week' : 'quota-short';
      chart.append(svgEl('circle', { cx: 28, cy: 28, r: radius, class: 'quota-track', fill: 'none', 'stroke-width': 3 }));
      // SVG circles run clockwise; rotate the used arc's start to noon.
      if (used > 0) chart.append(svgEl('circle', { cx: 28, cy: 28, r: radius, class: colorClass,
        fill: 'none', 'stroke-width': 3, pathLength: 100, 'stroke-dasharray': `${used} 100`,
        'stroke-linecap': 'round', transform: 'rotate(-90 28 28)' }));
      const expected = expectedRemainingPercent(window, now);
      if (expected !== null) {
        const angle = (100 - expected) / 100 * 2 * Math.PI - Math.PI / 2;
        const points = { x1: 28 + (radius - 1.3) * Math.cos(angle), y1: 28 + (radius - 1.3) * Math.sin(angle),
          x2: 28 + (radius + 1.3) * Math.cos(angle), y2: 28 + (radius + 1.3) * Math.sin(angle) };
        chart.append(svgEl('line', { ...points, class: 'quota-pace-outline', 'stroke-width': 3.5 }),
          svgEl('line', { ...points, class: 'quota-pace-marker', 'stroke-width': 1.7, 'stroke-linecap': 'round' }));
      }
      const text = svgEl('text', { x: 28, y: tracks.length === 1 ? 33 : index === 0 ? 25 : 39,
        class: remaining === null ? 'quota-unknown' : colorClass, 'text-anchor': 'middle' });
      text.textContent = remaining === null ? loading ? '…' : '—' : `${remaining}%`;
      chart.append(text);
      if (window) descriptions.push(`${window.label || '额度'}剩余 ${remaining}%`
        + (expected === null ? '' : ` · 均匀使用应已用 ${Math.round(100 - expected)}%`) + ` · ${resetText(window)}`);
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
      const expected = expectedRemainingPercent(w, now);
      if (expected !== null) row.append(el('small', '', `参考刻度：均匀使用应已用 ${Math.round(100 - expected)}%`));
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
    sourceSelect.addEventListener('change', sourceChange);
    documentRef.addEventListener?.('pointerdown', outside); documentRef.addEventListener?.('keydown', escape);
    if (lastRender) render(lastRender);
  }
  function stop() {
    if (!started) return; started = false; close(); root.hidden = true;
    syncPaceTimer(false); lastRender = null;
    button.removeEventListener?.('click', toggle); refresh.removeEventListener?.('click', refreshClick);
    sourceSelect.removeEventListener?.('change', sourceChange);
    documentRef.removeEventListener?.('pointerdown', outside); documentRef.removeEventListener?.('keydown', escape);
  }
  return { render, close, start, stop };
}
