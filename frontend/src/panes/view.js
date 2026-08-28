import { allPresets, normalizeLayoutMode, PANE_IDS } from './presets.js';

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
    const title = el('span', 'terminal-pane-title', '窗格 ' + paneNumber);
    const selector = el('select', 'terminal-pane-session');
    selector.dataset.paneId = paneId;
    selector.setAttribute?.('aria-label', title.textContent + '会话');
    const clear = el('button', 'terminal-pane-clear', '×');
    clear.type = 'button';
    clear.dataset.paneId = paneId;
    clear.title = '清空窗格（不会关闭会话）';
    clear.setAttribute?.('aria-label', clear.title);
    header.append(title, selector, clear);

    const body = el('div', 'terminal-pane-body');
    body.dataset.paneId = paneId;
    root.append(header, body);
    terminalRoot.appendChild(root);
    panes.set(paneId, { id: paneId, root, header, title, selector, clear, body });

    body.addEventListener?.('click', () => onPaneFocus?.(paneId));
    selector.addEventListener?.('click', () => onPaneFocus?.(paneId, { focus: false }));
    selector.addEventListener?.('change', () => onSessionChange?.(paneId, selector.value));
    clear.addEventListener?.('click', (event) => {
      event.stopPropagation?.();
      onClearPane?.(paneId);
    });
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
    for (const [itemMode, button] of layoutButtons) {
      const selected = itemMode === normalized;
      button.classList?.toggle('is-selected', selected);
      button.setAttribute?.('aria-checked', String(selected));
    }
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
      pane.title.textContent = selected ? selected.label : '窗格 ' + (Number(pane.id.slice(-1)) + 1);
      pane.clear.disabled = !paneStateItem.token;
    }
  }

  function setFocusedPane(paneId) {
    if (paneId) terminalRoot.dataset.focusedPaneId = paneId;
    else delete terminalRoot.dataset.focusedPaneId;
    for (const [id, pane] of panes) pane.root.classList?.toggle('is-focused', id === paneId);
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

  function paneBodies() {
    return [...panes.values()].map((pane) => ({ id: pane.id, body: pane.body }));
  }

  return {
    hostPool,
    layoutButton,
    layoutMenu,
    paneBody,
    paneBodies,
    paneRoot,
    paneSelector,
    setFocusedPane,
    setLayout,
    start,
    stop,
    updatePanes,
  };
}
