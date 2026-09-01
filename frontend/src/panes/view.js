import {
  allPresets,
  dividerVisibility,
  normalizeLayoutMode,
  PANE_IDS,
} from './presets.js';

function defaultElement(documentRef, tag, className, text) {
  const element = documentRef.createElement(tag);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
}

function setHidden(element, hidden) {
  element.hidden = hidden;
  element.setAttribute?.('aria-hidden', String(hidden));
}

export function createPaneView(deps) {
  const {
    documentRef,
    terminalRoot,
    statusBar,
    el = (tag, className, text) => defaultElement(documentRef, tag, className, text),
    onLayout,
    onPaneFocus,
    onSessionChange,
    onClearPane,
    onDividerPointerDown,
    onDividerKeydown,
  } = deps;

  terminalRoot.classList?.add('terminal-layout');
  const hostPool = el('div', 'terminal-host-pool');
  hostPool.setAttribute?.('aria-hidden', 'true');
  terminalRoot.appendChild(hostPool);

  const panes = new Map();
  for (const paneId of PANE_IDS) {
    const paneNumber = Number(paneId.slice(-1)) + 1;
    const root = el('section', 'terminal-pane-slot');
    root.dataset.paneId = paneId;
    root.setAttribute?.('aria-label', '终端窗格 ' + paneNumber);

    const header = el('div', 'terminal-pane-header');
    const current = el('span', 'terminal-pane-current', '当前');
    current.setAttribute?.('aria-hidden', 'true');
    const selector = el('select', 'terminal-pane-session');
    selector.dataset.paneId = paneId;
    selector.setAttribute?.('aria-label', '终端窗格 ' + paneNumber + '会话');
    const usageSummary = el('button', 'terminal-pane-usage-summary', '用量 —');
    usageSummary.type = 'button';
    usageSummary.disabled = true;
    usageSummary.setAttribute?.('aria-expanded', 'false');
    usageSummary.setAttribute?.('aria-controls', `terminal-pane-usage-details-${paneId}`);
    usageSummary.setAttribute?.('aria-label', '查看窗格 ' + paneNumber + ' token 用量');
    const usageDetails = el('div', 'terminal-pane-usage-details');
    usageDetails.id = `terminal-pane-usage-details-${paneId}`;
    usageDetails.setAttribute?.('role', 'dialog');
    usageDetails.setAttribute?.('aria-label', '窗格 ' + paneNumber + ' token 用量详情');
    setHidden(usageDetails, true);
    const clear = el('button', 'terminal-pane-clear', '×');
    clear.type = 'button';
    clear.dataset.paneId = paneId;
    clear.title = '清空窗格并收缩布局（不会关闭会话）';
    clear.setAttribute?.('aria-label', clear.title);
    header.append(current, selector, usageSummary, clear);

    const body = el('div', 'terminal-pane-body');
    body.dataset.paneId = paneId;
    root.append(header, usageDetails, body);
    terminalRoot.appendChild(root);
    panes.set(paneId, {
      id: paneId,
      root,
      header,
      current,
      selector,
      usageSummary,
      usageDetails,
      clear,
      body,
    });

    body.addEventListener?.('click', () => onPaneFocus?.(paneId));
    selector.addEventListener?.('click', () => onPaneFocus?.(paneId, { focus: false }));
    selector.addEventListener?.('change', () => onSessionChange?.(paneId, selector.value));
    clear.addEventListener?.('click', (event) => {
      event.stopPropagation?.();
      onClearPane?.(paneId);
    });
  }

  const dividers = new Map();
  for (const axis of ['vertical', 'horizontal']) {
    const divider = el('div', `terminal-pane-divider terminal-pane-divider-${axis}`);
    divider.dataset.dividerAxis = axis;
    divider.tabIndex = 0;
    divider.setAttribute?.('role', 'separator');
    divider.setAttribute?.('aria-orientation', axis);
    divider.setAttribute?.(
      'aria-label',
      axis === 'vertical' ? '调整左右窗格宽度' : '调整上下窗格高度',
    );
    divider.setAttribute?.('aria-valuemin', '16');
    divider.setAttribute?.('aria-valuemax', '84');
    divider.setAttribute?.('aria-valuenow', '50');
    setHidden(divider, true);
    divider.addEventListener?.('pointerdown', (event) => onDividerPointerDown?.(axis, event));
    divider.addEventListener?.('keydown', (event) => onDividerKeydown?.(axis, event));
    terminalRoot.appendChild(divider);
    dividers.set(axis, divider);
  }

  const layoutControl = el('div', 'terminal-layout-control');
  const layoutButton = el('button', 'terminal-layout-button', '布局');
  layoutButton.type = 'button';
  layoutButton.setAttribute?.('aria-haspopup', 'menu');
  layoutButton.setAttribute?.('aria-expanded', 'false');
  layoutButton.title = '选择终端布局';
  const layoutMenu = el('div', 'terminal-layout-menu');
  layoutMenu.setAttribute?.('role', 'menu');
  setHidden(layoutMenu, true);
  const layoutButtons = new Map();
  for (const preset of allPresets()) {
    const item = el('button', 'terminal-layout-option', preset.label);
    item.type = 'button';
    item.dataset.layoutMode = preset.mode;
    item.setAttribute?.('role', 'menuitemradio');
    item.setAttribute?.('aria-checked', 'false');
    item.addEventListener?.('click', (event) => {
      event.stopPropagation?.();
      onLayout?.(preset.mode);
      setHidden(layoutMenu, true);
      layoutButton.setAttribute?.('aria-expanded', 'false');
    });
    layoutButtons.set(preset.mode, item);
    layoutMenu.appendChild(item);
  }
  layoutControl.append(layoutButton, layoutMenu);
  statusBar.appendChild(layoutControl);

  let documentClickBound = false;
  const onDocumentClick = (event) => {
    if (event.target === layoutButton || layoutControl.contains?.(event.target)) return;
    setHidden(layoutMenu, true);
    layoutButton.setAttribute?.('aria-expanded', 'false');
  };
  const onDocumentKeydown = (event) => {
    if (event.key !== 'Escape') return;
    setHidden(layoutMenu, true);
    layoutButton.setAttribute?.('aria-expanded', 'false');
  };
  const toggleMenu = (event) => {
    event.stopPropagation?.();
    const next = layoutMenu.hidden;
    setHidden(layoutMenu, !next);
    layoutButton.setAttribute?.('aria-expanded', String(next));
  };
  layoutButton.addEventListener?.('click', toggleMenu);

  function start() {
    if (documentClickBound) return;
    documentClickBound = true;
    documentRef.addEventListener?.('click', onDocumentClick);
    documentRef.addEventListener?.('keydown', onDocumentKeydown);
  }

  function stop() {
    if (!documentClickBound) return;
    documentClickBound = false;
    documentRef.removeEventListener?.('click', onDocumentClick);
    documentRef.removeEventListener?.('keydown', onDocumentKeydown);
  }

  function setLayout(mode) {
    const normalized = normalizeLayoutMode(mode);
    terminalRoot.dataset.layoutMode = normalized;
    terminalRoot.classList?.forEach?.((name) => {
      if (name.startsWith('layout-mode-')) terminalRoot.classList.remove(name);
    });
    terminalRoot.classList?.add('layout-mode-' + normalized);
    const visible = new Set(allPresets().find((item) => item.mode === normalized).visiblePaneIds);
    for (const [paneId, pane] of panes) {
      const isVisible = visible.has(paneId);
      pane.root.classList?.toggle('is-visible', isVisible);
      setHidden(pane.root, !isVisible);
    }
    const visibility = dividerVisibility(normalized);
    for (const [axis, divider] of dividers) {
      setHidden(divider, !visibility[axis]);
    }
    for (const [itemMode, button] of layoutButtons) {
      const selected = itemMode === normalized;
      button.classList?.toggle('is-selected', selected);
      button.setAttribute?.('aria-checked', String(selected));
    }
  }

  function setSplitRatios(ratios = {}) {
    const properties = {
      vertical: ['--terminal-split-columns-first', '--terminal-split-columns-second'],
      horizontal: ['--terminal-split-rows-first', '--terminal-split-rows-second'],
    };
    for (const [axis, [firstProperty, secondProperty]] of Object.entries(properties)) {
      const values = ratios[axis] || {};
      for (const [property, value] of [[firstProperty, values.first], [secondProperty, values.second]]) {
        if (!Number.isFinite(value) || value <= 0) continue;
        terminalRoot.style?.setProperty?.(property, `${value}fr`);
      }
      const divider = dividers.get(axis);
      if (divider && Number.isFinite(values.first)) {
        divider.setAttribute?.('aria-valuenow', String(Math.round(values.first * 100)));
      }
    }
  }

  function setDividerDragging(axis, dragging) {
    dividers.get(axis)?.classList?.toggle('is-dragging', dragging);
    terminalRoot.classList?.toggle('is-resizing', dragging);
  }

  function updatePanes(paneState, sessionOptions = []) {
    const options = [{ token: '', label: '选择运行中的会话' }, ...sessionOptions];
    for (const paneStateItem of paneState) {
      const pane = panes.get(paneStateItem.id);
      if (!pane) continue;
      pane.selector.replaceChildren?.();
      for (const optionInfo of options) {
        const option = el('option');
        option.value = optionInfo.token;
        option.textContent = optionInfo.label;
        option.disabled = Boolean(optionInfo.disabled);
        pane.selector.appendChild(option);
      }
      pane.selector.value = paneStateItem.token || '';
      const selected = sessionOptions.find((item) => item.token === paneStateItem.token);
      pane.usageSummary.disabled = true;
      pane.usageSummary.setAttribute?.(
        'aria-label',
        selected
          ? '查看 ' + selected.label + ' token 用量'
          : '查看窗格 ' + (Number(pane.id.slice(-1)) + 1) + ' token 用量',
      );
      pane.clear.disabled = !paneStateItem.token;
    }
  }

  function setFocusedPane(paneId) {
    if (paneId) terminalRoot.dataset.focusedPaneId = paneId;
    else delete terminalRoot.dataset.focusedPaneId;
    for (const [id, pane] of panes) {
      const focused = id === paneId;
      pane.root.classList?.toggle('is-focused', focused);
      pane.current.setAttribute?.('aria-hidden', String(!focused));
    }
  }

  function paneBody(paneId) {
    return panes.get(paneId)?.body || null;
  }

  function paneRoot(paneId) {
    return panes.get(paneId)?.root || null;
  }

  function paneSelector(paneId) {
    return panes.get(paneId)?.selector || null;
  }

  function paneUsageSummary(paneId) {
    return panes.get(paneId)?.usageSummary || null;
  }

  function paneUsageDetails(paneId) {
    return panes.get(paneId)?.usageDetails || null;
  }

  function divider(axis) {
    return dividers.get(axis) || null;
  }

  function paneBodies() {
    return [...panes.values()].map((pane) => ({ id: pane.id, body: pane.body }));
  }

  function paneSurfaces() {
    return [...panes.values()].map(({ id, current, selector, usageSummary, usageDetails, clear, body, header, root }) => ({
      id,
      current,
      selector,
      usageSummary,
      usageDetails,
      clear,
      body,
      header,
      root,
    }));
  }

  return {
    hostPool,
    divider,
    layoutButton,
    layoutMenu,
    paneBody,
    paneBodies,
    paneRoot,
    paneSelector,
    paneSurfaces,
    paneUsageDetails,
    paneUsageSummary,
    setDividerDragging,
    setSplitRatios,
    setFocusedPane,
    setLayout,
    start,
    stop,
    updatePanes,
  };
}
