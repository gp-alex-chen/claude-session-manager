import { leafOf } from '../utils.js';

export function renderSessionList({
  listRoot,
  list,
  state,
  agentController,
  el,
  onStartNew,
  onToggleGroup,
  onOpen,
  onClose,
  onContextMenu,
}) {
  const groups = new Map();
  for (const session of list) {
    state.sessionNames.set(session.id, session.name);
    if (!groups.has(session.dir)) groups.set(session.dir, []);
    groups.get(session.dir).push(session);
  }

  listRoot.innerHTML = '';
  for (const [dir, items] of groups) {
    const group = el('div', 'group');
    if (state.collapsedDirs.has(dir)) group.classList.add('collapsed');
    const head = el('div', 'group-head');
    const chevron = el('span', 'chevron');
    chevron.title = '点击折叠/展开';
    const name = el('span', 'group-name', leafOf(dir));
    name.title = dir;
    const plus = el('button', 'plus', '+');
    plus.title = '在 ' + dir + ' 新建会话';
    plus.addEventListener('click', (event) => {
      event.stopPropagation();
      onStartNew(dir);
    });
    head.addEventListener('click', () => onToggleGroup(dir, group, chevron));
    head.append(chevron, name, plus);
    group.appendChild(head);

    const body = el('div', 'group-body');
    for (const session of items) {
      body.appendChild(renderSessionRow({
        session,
        state,
        agentController,
        el,
        onOpen,
        onClose,
        onContextMenu,
      }));
    }
    group.appendChild(body);
    listRoot.appendChild(group);
  }
}

function renderSessionRow({
  session,
  state,
  agentController,
  el,
  onOpen,
  onClose,
  onContextMenu,
}) {
  const item = el('div', 'session-item');
  item.dataset.id = session.id;
  item.dataset.dir = session.dir;
  item.title = session.dir;
  if (state.collapsedDirs.has(session.dir)
    && (state.eyeGlobalOff || agentController.classifyAgent(session.id) === 'idle')) {
    item.classList.add('fold-hidden');
  }

  const nameRow = el('div', 's-name');
  const badge = el('span', 'badge idle', '●');
  badge.dataset.id = session.id;
  badge.title = '未运行';
  nameRow.append(badge, el('span', 's-name-text', session.name));

  const closeButton = el('span', 's-close', '×');
  closeButton.title = '关闭此终端（结束进程）';
  closeButton.addEventListener('click', (event) => {
    event.stopPropagation();
    onClose(session.id);
  });
  nameRow.appendChild(closeButton);
  item.appendChild(nameRow);
  item.appendChild(el('div', 's-time', session.time));
  item.addEventListener('click', () => onOpen(session));
  item.addEventListener('contextmenu', (event) => {
    event.preventDefault();
    onContextMenu(event.clientX, event.clientY, {
      type: 'session', id: session.id, dir: session.dir, name: session.name,
    });
  });
  return item;
}

export function renderHiddenSessions({
  hiddenPanel,
  list,
  el,
  onRestore,
}) {
  hiddenPanel.innerHTML = '';
  if (!list.length) {
    hiddenPanel.appendChild(el('div', 'hidden-item', '（无归档会话）'));
    return;
  }
  for (const session of list) {
    const row = el('div', 'hidden-item');
    const name = el('span', 'h-name', session.name);
    name.title = session.dir;
    row.append(name, el('span', 'h-dir', leafOf(session.dir)));
    const restore = el('button', '', '恢复');
    restore.title = '在列表中重新显示此会话';
    restore.addEventListener('click', () => onRestore(session));
    row.appendChild(restore);
    hiddenPanel.appendChild(row);
  }
}
