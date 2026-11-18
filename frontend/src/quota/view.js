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

// A group owns all its windows. The renderer handles any number of groups
// and layers; provider adapters establish the actual group identities.
export function quotaRingGroups(windows = [], { includeScoped = false } = {}) {
  const groups = new Map();
  const visible = includeScoped ? windows.filter(w => remainingPercent(w) !== null) : ringWindows(windows);
  for (const window of visible) {
    const id = window.scope || '';
    if (!groups.has(id)) groups.set(id, { id, label: id, windows: [] });
    groups.get(id).windows.push(window);
  }
  return [...groups.values()].sort((a, b) => a.id < b.id ? -1 : a.id > b.id ? 1 : 0).map(group => ({
    ...group,
    label: group.windows.length === 1 && !(group.windows[0].windowMinutes > 0)
      ? group.windows[0].label || group.label : group.label,
    windows: group.windows.sort((a, b) => (a.windowMinutes > 0 ? a.windowMinutes : Infinity)
      - (b.windowMinutes > 0 ? b.windowMinutes : Infinity) || String(a.id || a.label || '').localeCompare(String(b.id || b.label || ''))),
  }));
}

function resetText(window) {
  return window.resetsAt > 0 ? new Date(window.resetsAt * 1000).toLocaleString('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }) + ' 重置' : '重置时间未知';
}

export function createQuotaView({ root, documentRef, onRefresh, onSourceChange, onViewResetCredits, onConsume, nowFn = () => Date.now(),
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
  for (const [value, text] of [['auto', '自动跟随会话'], ['codex', 'Codex / ChatGPT'], ['claude', 'Claude'], ['antigravity', 'Antigravity']]) {
    const option = el('option', '', text); option.value = value; sourceSelect.append(option);
  }
  sourceField.append(el('span', '', '额度来源'), sourceSelect);
  const sourceNote = el('p', 'quota-source-note', '读取本机所选 CLI 当前登录账户的订阅额度。');
  const content = el('div');
  const resets = el('div', 'quota-reset-credits'); resets.setAttribute('aria-live', 'polite');
  let confirmation = null; let resetIdentity = ''; let renderedProvider = null;
  const refresh = el('button', 'quota-refresh', '刷新额度'); refresh.type = 'button';
  panel.append(sourceField, sourceNote, content, resets, refresh); root.append(button, panel); root.hidden = true;
  function close() { panel.hidden = true; button.setAttribute('aria-expanded', 'false'); confirmation = null; resets.replaceChildren(); }
  const toggle = () => { if (!panel.hidden) { close(); return; } panel.hidden = false; button.setAttribute('aria-expanded', 'true'); if (lastRender) render(lastRender); if (lastRender?.provider === 'antigravity') onRefresh?.(); };
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
    if (renderedProvider !== provider) confirmation = null;
    renderedProvider = provider;
    sourceSelect.value = source;
    root.hidden = !provider;
    if (!provider) { syncPaceTimer(false); close(); return; }
    const name = provider === 'codex' ? 'Codex' : provider === 'claude' ? 'Claude' : provider === 'antigravity' ? 'Antigravity' : provider;
    sourceNote.textContent = provider === 'antigravity'
      ? '收到新的 Antigravity 额度回调后自动刷新。可在终端运行 /usage 获取新额度。'
      : '读取本机所选 CLI 当前登录账户的订阅额度。';
    label.textContent = name;
    const windows = snapshot?.status === 'available' ? snapshot.windows || [] : [];
    const groups = quotaRingGroups(windows, { includeScoped: provider === 'antigravity' });
    const selected = groups.flatMap(group => group.windows);
    const stale = !!snapshot?.stale || !!error;
    root.classList.toggle('quota-stale', stale);
    button.setAttribute('aria-busy', String(loading));
    const descriptions = [];
    const now = nowFn();
    syncPaceTimer(selected.some(window => expectedRemainingPercent(window, now) !== null));
    const meters = (groups.length ? groups : [{ label: '', windows: [] }]).map(group => {
      const tracks = group.windows.length ? group.windows : [null];
      const outerRadius = tracks.length <= 2 ? 25 : Math.max(20, tracks.length * 6 + 4) + (tracks.length - 1) * 5;
      const size = outerRadius * 2 + 6;
      const center = size / 2;
      const pixels = Math.round(size * 36 / 56);
      const chart = svgEl('svg', { viewBox: `0 0 ${size} ${size}`, width: pixels, height: pixels,
        class: tracks.length > 2 ? 'quota-many-layers' : '', 'data-quota-group': group.id || '' });
      const groupDescriptions = [];
      tracks.forEach((window, index) => {
        const radius = outerRadius - index * 5;
        const remaining = remainingPercent(window);
        const used = remaining === null ? null : 100 - remaining;
        const colorClass = window?.windowMinutes === 10080 ? 'quota-week' : 'quota-short';
        chart.append(svgEl('circle', { cx: center, cy: center, r: radius, class: 'quota-track', fill: 'none', 'stroke-width': 3 }));
        // SVG circles run clockwise; rotate the used arc's start to noon.
        if (used > 0) chart.append(svgEl('circle', { cx: center, cy: center, r: radius, class: colorClass,
          fill: 'none', 'stroke-width': 3, pathLength: 100, 'stroke-dasharray': `${used} 100`,
          'stroke-linecap': 'round', transform: `rotate(-90 ${center} ${center})` }));
        const expected = expectedRemainingPercent(window, now);
        if (expected !== null) {
          const angle = (100 - expected) / 100 * 2 * Math.PI - Math.PI / 2;
          const points = { x1: center + (radius - 1.3) * Math.cos(angle), y1: center + (radius - 1.3) * Math.sin(angle),
            x2: center + (radius + 1.3) * Math.cos(angle), y2: center + (radius + 1.3) * Math.sin(angle) };
          chart.append(svgEl('line', { ...points, class: 'quota-pace-outline', 'stroke-width': 3.5 }),
            svgEl('line', { ...points, class: 'quota-pace-marker', 'stroke-width': 1.7, 'stroke-linecap': 'round' }));
        }
        const text = svgEl('text', { x: center, y: tracks.length === 1 ? center + 5
          : tracks.length === 2 ? center + (index === 0 ? -3 : 11) : center - (tracks.length - 1) * 6 + index * 12 + 4,
          class: remaining === null ? 'quota-unknown' : colorClass, 'text-anchor': 'middle' });
        text.textContent = remaining === null ? loading ? '…' : '—' : `${remaining}%`;
        chart.append(text);
        if (window) groupDescriptions.push(`${window.label || '额度'}剩余 ${remaining}%`
          + (expected === null ? '' : ` · 均匀使用应已用 ${Math.round(100 - expected)}%`) + ` · ${resetText(window)}`);
      });
      descriptions.push(...groupDescriptions);
      const groupNode = el('span', 'quota-ring-group');
      groupNode.title = [group.label, ...groupDescriptions].filter(Boolean).join('\n');
      groupNode.append(chart);
      if (group.label) groupNode.append(el('span', 'quota-ring-group-label', group.label));
      return groupNode;
    });
    meter.replaceChildren(...meters);
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
    if (!windows.length || stale || provider === 'antigravity') rows.push(el('p', 'quota-message', message));
    if (snapshot?.readAt && !String(snapshot.readAt).startsWith('0001')) rows.push(el('small', 'quota-read-time',
      `${stale ? '上次读取' : '读取于'} ${new Date(snapshot.readAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}`));
    content.replaceChildren(title, ...rows);
    refresh.disabled = loading || (provider === 'codex' && (input.resetLoading || input.resetUsing)); refresh.textContent = loading || input.resetLoading ? '查询中…' : provider === 'antigravity' ? '读取终端额度' : '刷新额度';
    renderResets(input);
  }
  function renderResets(input) {
    resets.replaceChildren();
    resets.hidden = input.provider !== 'codex';
    if (resets.hidden) return;
    const detail = input.resetDetails;
    const token = detail?.offerToken || '';
    if (resetIdentity !== token || input.resetLoading || input.resetUsing) confirmation = null;
    resetIdentity = token;
    const summary = Object.hasOwn(input.snapshot || {}, 'resetCredits')
      ? input.snapshot.resetCredits : detail?.snapshot?.resetCredits;
    const rows = [el('div', 'quota-details-title', '重置卡')];
    const action = (text, className, callback) => {
      const node = el('button', className, text); node.type = 'button';
      node.disabled = !!input.resetLoading || !!input.resetUsing;
      node.addEventListener('click', callback); return node;
    };
    rows.push(action(input.resetLoading ? '读取中…' : '查看重置卡', 'quota-credit-view', () => onViewResetCredits?.()));
    if (input.resetMessage) rows.push(el('p', 'quota-message', input.resetMessage));
    if (detail?.message) rows.push(el('p', 'quota-message', detail.message));
    if (input.resetUsing) rows.push(el('p', 'quota-message', '正在处理，请勿重复操作…'));
    else if (input.resetLoading) rows.push(el('p', 'quota-message', '正在读取重置卡…'));
    if (!detail) {
      if (!input.resetLoading) rows.push(el('p', 'quota-message', '点击查看，读取当前可用重置卡。'));
      resets.replaceChildren(...rows); return;
    }
    if (Number.isInteger(summary?.availableCount) && summary.availableCount >= 0) {
      rows.push(el('p', 'quota-credit-count', `可用 ${summary.availableCount} 张`));
    } else if (!input.resetLoading) rows.push(el('p', 'quota-message', '当前账户或 CLI 尚未提供重置卡信息。'));
    if (detail?.pending) {
      rows.push(action('核对上次使用结果', 'quota-credit-retry', () => onConsume?.(token, detail.pendingCreditId || '')));
    } else if (summary) {
      const credits = Array.isArray(summary.credits) ? summary.credits : [];
      for (const credit of credits) {
        const row = el('div', 'quota-credit');
        row.append(el('strong', '', credit.title || '重置卡'));
        row.append(el('small', '', credit.resetType === 'codexRateLimits' ? 'Codex 额度重置' : '未知重置类型'));
        if (credit.description) row.append(el('p', 'quota-message', credit.description));
        const expiry = credit.expiresAt;
        row.append(el('small', '', expiry == null ? '无到期时间' : `到期：${new Date(expiry * 1000).toLocaleString('zh-CN')}`));
        const eligible = credit.status === 'available' && credit.resetType === 'codexRateLimits'
          && (expiry == null || (Number.isFinite(expiry) && expiry * 1000 > nowFn()));
        if (eligible && token && !input.resetUsing) row.append(action('使用这张', 'quota-credit-use', () => {
          confirmation = { token, id: credit.id, text: credit.title || 'Codex 额度重置卡' }; render(lastRender);
        }));
        else if (!eligible) row.append(el('small', '', '当前不可使用'));
        rows.push(row);
      }
      if (summary.availableCount > credits.length && credits.length) rows.push(el('p', 'quota-message', '仅展示服务返回的部分卡片，可用总数以账户返回值为准。'));
      if (summary.availableCount > 0 && !credits.length && !detail?.pending) {
        rows.push(el('p', 'quota-message', summary.credits == null ? '当前未提供卡片详情。' : '服务未返回可展示的卡片详情。'));
        if (token && !input.resetUsing) rows.push(action('使用下一张', 'quota-credit-use', () => {
          confirmation = { token, id: '', text: '由服务端选择下一张；当前未提供该卡类型和到期信息' }; render(lastRender);
        }));
      }
    }
    if (confirmation && token === confirmation.token && !input.resetLoading && !input.resetUsing) {
      const chosen = confirmation;
      const box = el('div', 'quota-credit-confirmation');
      box.append(el('p', '', `确认使用 1 张重置卡？${chosen.text}。将请求重置符合条件的额度窗口。`));
      box.append(action('确认使用', 'quota-credit-confirm', () => {
        confirmation = null; onConsume?.(chosen.token, chosen.id); render(lastRender);
      }), action('取消', 'quota-credit-cancel', () => { confirmation = null; render(lastRender); }));
      rows.push(box);
    }
    resets.replaceChildren(...rows);
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
