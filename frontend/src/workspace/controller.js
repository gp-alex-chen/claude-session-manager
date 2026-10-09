import { leafOf } from '../utils.js';

const WIDTH_KEY = 'sidebar-width';
const COLLAPSED_KEY = 'sidebar-collapsed';
export function sidebarWidth(value) {
  const number = Number(value);
  return Number.isFinite(number) && value !== null && value !== ''
    ? Math.round(Math.max(220, Math.min(360, number))) : 280;
}

export function createWorkspaceController({ state, nodes, storage, documentRef,
  onResize, setTimeoutFn = setTimeout, clearTimeoutFn = clearTimeout }) {
  let width = 280;
  let collapsed = false;
  let started = false;
  let drag = null;
  let statusTimer = null;
  const bindings = [];
  const bind = (node, event, callback) => {
    node.addEventListener?.(event, callback);
    bindings.push([node, event, callback]);
  };

  function render() {
    nodes.app.style.setProperty?.('--sidebar-width', width + 'px');
    nodes.app.classList.toggle('sidebar-collapsed', collapsed);
    nodes.sidebar.hidden = collapsed;
    nodes['sidebar-divider'].hidden = collapsed;
    nodes['sidebar-divider'].setAttribute('aria-valuenow', String(width));
    const label = collapsed ? '展开侧栏' : '收起侧栏';
    nodes['btn-sidebar-toggle'].title = label;
    nodes['btn-sidebar-toggle'].setAttribute('aria-label', label);
    nodes['btn-sidebar-toggle'].setAttribute('aria-expanded', String(!collapsed));
  }

  function refreshContext() {
    const terminal = state.terminals.get(state.activeToken);
    const dir = terminal?.dir || state.sessionDirs.get(state.newToReal.get(state.activeToken) || state.activeToken) || '';
    nodes['workspace-project'].textContent = dir ? leafOf(dir) : '终端工作区';
    nodes['workspace-project'].title = dir || '选择会话或新建终端';
    nodes['workspace-directory'].textContent = dir;
    nodes['workspace-directory'].title = dir;
    const opened = [...state.terminals.values()].filter((item) => item && !item.exited).length;
    nodes['workspace-summary'].textContent = opened + ' 个会话已打开';
  }

  function setWidth(value, persist = true) {
    width = sidebarWidth(value);
    render();
    if (persist) storage?.setItem(WIDTH_KEY, String(width));
    onResize?.();
  }

  function finishDrag(event) {
    if (!drag || (event?.pointerId !== undefined && event.pointerId !== drag.id)) return;
    drag = null;
    nodes.app.classList.remove('sidebar-resizing');
    nodes['sidebar-divider'].classList.remove('is-dragging');
    documentRef.removeEventListener?.('pointermove', moveDrag);
    documentRef.removeEventListener?.('pointerup', finishDrag);
    documentRef.removeEventListener?.('pointercancel', finishDrag);
    storage?.setItem(WIDTH_KEY, String(width));
    onResize?.();
  }

  function moveDrag(event) {
    if (!drag || event.pointerId !== drag.id || !Number.isFinite(event.clientX)) return;
    setWidth(drag.width + event.clientX - drag.x, false);
    event.preventDefault?.();
  }

  function startDrag(event) {
    if (collapsed || (event.button !== undefined && event.button !== 0) || !Number.isFinite(event.clientX)) return;
    finishDrag();
    drag = { id: event.pointerId, x: event.clientX, width };
    nodes.app.classList.add('sidebar-resizing');
    nodes['sidebar-divider'].classList.add('is-dragging');
    documentRef.addEventListener?.('pointermove', moveDrag);
    documentRef.addEventListener?.('pointerup', finishDrag);
    documentRef.addEventListener?.('pointercancel', finishDrag);
    event.preventDefault?.();
  }

  function toggleSidebar() {
    finishDrag();
    collapsed = !collapsed;
    storage?.setItem(COLLAPSED_KEY, String(collapsed));
    render();
    onResize?.();
  }

  function setStatus(message, kind) {
    if (statusTimer !== null) clearTimeoutFn(statusTimer);
    statusTimer = null;
    nodes['status-message'].textContent = String(message);
    nodes['status-message'].className = kind || '';
    nodes['status-message'].title = String(message);
    if (kind === 'ok' && started) statusTimer = setTimeoutFn(() => {
      statusTimer = null;
      nodes['status-message'].textContent = '';
      nodes['status-message'].title = '';
      nodes['status-message'].className = '';
    }, 5000);
  }

  function initialize() {
    width = sidebarWidth(storage?.getItem(WIDTH_KEY));
    collapsed = storage?.getItem(COLLAPSED_KEY) === 'true';
    render();
    refreshContext();
  }

  function start() {
    if (started) return;
    started = true;
    bind(nodes['btn-sidebar-toggle'], 'click', toggleSidebar);
    bind(nodes['sidebar-divider'], 'pointerdown', startDrag);
    bind(nodes['sidebar-divider'], 'keydown', (event) => {
      if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
      event.preventDefault?.();
      setWidth(event.key === 'Home' ? 220 : event.key === 'End' ? 360
        : width + (event.key === 'ArrowLeft' ? -1 : 1) * (event.shiftKey ? 30 : 10));
    });
  }

  function stop() {
    if (!started) return;
    finishDrag();
    started = false;
    for (const [node, event, callback] of bindings.splice(0)) node.removeEventListener?.(event, callback);
    if (statusTimer !== null) clearTimeoutFn(statusTimer);
    statusTimer = null;
  }

  return { initialize, start, stop, refreshContext, setStatus, setWidth, toggleSidebar };
}
