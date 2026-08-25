import { leafOf } from '../utils.js';
import { formatProjectUsage, formatProjectUsageTitle } from '../usage/format.js';

export function dirIdentity(dir) {
  if (typeof dir !== 'string') return '';
  let normalized = dir.trim().replaceAll('\\', '/');
  if (!normalized) return '';

  const unc = normalized.startsWith('//');
  normalized = unc
    ? '//' + normalized.slice(2).replace(/\/+/g, '/')
    : normalized.replace(/\/+/g, '/');
  if (normalized === '/') return normalized;
  if (/^[A-Za-z]:\/$/.test(normalized)) return normalized.toLowerCase();
  normalized = normalized.replace(/\/+$/, '');
  if (!normalized) return '/';
  if (/^[A-Za-z]:\//.test(normalized) || normalized.startsWith('//')) {
    return normalized.toLowerCase();
  }
  return normalized;
}

function projectUsage(usageByProject, dir, identity = dirIdentity(dir)) {
  if (!usageByProject || typeof usageByProject.get !== 'function') return undefined;
  if (usageByProject.has(dir)) return usageByProject.get(dir);
  for (const [projectDir, summary] of usageByProject) {
    if (dirIdentity(projectDir) === identity) return summary;
  }
  return undefined;
}

export function renderSessionList({
  listRoot,
  list = [],
  projects = [],
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
    const identity = dirIdentity(session.dir);
    if (!groups.has(identity)) {
      groups.set(identity, {
        identity,
        dir: session.dir,
        projectDir: null,
        items: [],
      });
    }
    groups.get(identity).items.push(session);
  }
  for (const projectDir of Array.isArray(projects) ? projects : []) {
    const identity = dirIdentity(projectDir);
    const existing = groups.get(identity);
    if (existing) {
      if (!existing.projectDir) existing.projectDir = projectDir;
      continue;
    }
    groups.set(identity, {
      identity,
      dir: projectDir,
      projectDir,
      items: [],
    });
  }

  listRoot.innerHTML = '';
  for (const { identity, dir, projectDir, items } of groups.values()) {
    const startDir = projectDir || dir;
    const group = el('div', 'group');
    if (state.collapsedDirs.has(identity)) group.classList.add('collapsed');
    const head = el('div', 'group-head');
    head.dataset.dir = identity;
    const chevron = el('span', 'chevron');
    chevron.title = '点击折叠/展开';
    const name = el('span', 'group-name', leafOf(dir));
    name.title = dir;
    const summary = projectUsage(usageByProject, dir, identity);
    const usage = el('span', 'group-usage', formatProjectUsage(summary));
    usage.title = formatProjectUsageTitle(summary);
    const plus = el('button', 'plus', '+');
    plus.title = '在 ' + startDir + ' 新建会话';
    plus.addEventListener('click', (event) => {
      event.stopPropagation();
      onStartNew(startDir);
    });
    head.addEventListener('click', () => onToggleGroup(identity, group, chevron));
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
    const identity = dirIdentity(head.dataset.dir || '');
    const usage = head.querySelector?.('.group-usage');
    const summary = projectUsage(usageByProject, head.dataset.dir || '', identity);
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
  item.dataset.dir = dirIdentity(session.dir);
  item.title = session.dir;
  if (state.collapsedDirs.has(dirIdentity(session.dir))
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
