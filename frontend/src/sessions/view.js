import { leafOf } from '../utils.js';
import { formatProjectUsage, formatProjectUsageTitle } from '../usage/format.js';

export function renderProjectBar({ listRoot, projects = [], el, onStartNew, onDeleteProject }) {
  listRoot.innerHTML = '';
  if (!projects.length) {
    listRoot.appendChild(el('div', 'project-empty', '暂无项目'));
    return;
  }

  for (const dir of projects) {
    const item = el('div', 'project-item');
    const name = el('span', 'project-name', leafOf(dir));
    name.title = dir;
    const plus = el('button', 'project-plus', '+');
    plus.type = 'button';
    plus.title = '在 ' + dir + ' 新建会话';
    if (typeof onStartNew === 'function') {
      plus.addEventListener('click', (event) => {
        event.stopPropagation();
        return onStartNew(dir);
      });
    }
    const deleteButton = el('button', 'project-delete', '×');
    deleteButton.type = 'button';
    deleteButton.title = '移除项目';
    if (typeof onDeleteProject === 'function') {
      deleteButton.addEventListener('click', (event) => {
        event.stopPropagation();
        return onDeleteProject(dir);
      });
    }
    item.append(name, plus, deleteButton);
    listRoot.appendChild(item);
  }
}

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
  usageByProject = new Map(),
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
    head.dataset.dir = dir;
    const chevron = el('span', 'chevron');
    chevron.title = '点击折叠/展开';
    const name = el('span', 'group-name', leafOf(dir));
    name.title = dir;
    const usage = el('span', 'group-usage', formatProjectUsage(usageByProject.get(dir)));
    usage.title = formatProjectUsageTitle(usageByProject.get(dir));
    const plus = el('button', 'plus', '+');
    plus.title = '在 ' + dir + ' 新建会话';
    plus.addEventListener('click', (event) => {
      event.stopPropagation();
      onStartNew(dir);
    });
    head.addEventListener('click', () => onToggleGroup(dir, group, chevron));
    head.append(chevron, name, usage, plus);
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

export function updateProjectUsageLabels({ listRoot, usageByProject }) {
  for (const head of listRoot.querySelectorAll('.group-head')) {
    const dir = head.dataset.dir || '';
    const usage = head.querySelector?.('.group-usage');
    const summary = usageByProject.get(dir);
    if (!usage) continue;
    usage.textContent = formatProjectUsage(summary);
    usage.title = formatProjectUsageTitle(summary);
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
