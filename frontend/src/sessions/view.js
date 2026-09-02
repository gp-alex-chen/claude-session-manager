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
  favoriteProjects = [],
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

  const favoriteOrder = new Map();
  for (const projectDir of Array.isArray(favoriteProjects) ? favoriteProjects : []) {
    const identity = dirIdentity(projectDir);
    if (identity && !favoriteOrder.has(identity)) favoriteOrder.set(identity, favoriteOrder.size);
  }
  const orderedGroups = [...groups.values()];
  orderedGroups.forEach((group, index) => { group.order = index; });
  orderedGroups.sort((left, right) => {
    const leftRank = favoriteOrder.get(left.identity);
    const rightRank = favoriteOrder.get(right.identity);
    if (leftRank !== undefined || rightRank !== undefined) {
      if (leftRank === undefined) return 1;
      if (rightRank === undefined) return -1;
      return leftRank - rightRank;
    }
    return left.order - right.order;
  });

  listRoot.innerHTML = '';
  for (const { identity, dir, projectDir, items } of orderedGroups) {
    const startDir = projectDir || dir;
    const isProject = Boolean(projectDir);
    const isFavorite = favoriteOrder.has(identity);
    const group = el('div', 'group');
    if (state.collapsedDirs.has(identity)) group.classList.add('collapsed');
    const head = el('div', 'group-head');
    head.dataset.dir = identity;
    const folder = el('span', 'folder-icon');
    folder.classList.toggle('favorite', isFavorite);
    folder.title = isFavorite ? '已收藏项目' : (isProject ? '项目文件夹' : '会话目录');
    folder.setAttribute('aria-hidden', 'true');
    folder.innerHTML = [
      '<svg class="folder-open" xmlns="http://www.w3.org/2000/svg" width="16" height="16"',
      ' viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"',
      ' stroke-linecap="round" stroke-linejoin="round"><path d="m6 14 1.5-2.9',
      'A2 2 0 0 1 9.24 10H20a2 2 0 0 1 1.94 2.5l-1.54 6a2 2 0 0 1-1.95',
      ' 1.5H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h3.9a2 2 0 0 1 1.69.9l.81',
      ' 1.2a2 2 0 0 0 1.67.9H18a2 2 0 0 1 2 2v2"/></svg>',
      '<svg class="folder-closed" xmlns="http://www.w3.org/2000/svg" width="16" height="16"',
      ' viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"',
      ' stroke-linecap="round" stroke-linejoin="round"><path d="M20 20a2 2 0 0 0 2-2',
      'V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4',
      'a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/><path d="M2 10h20"/></svg>',
    ].join('');
    const name = el('span', 'group-name', leafOf(dir));
    name.title = dir;
    head.dataset.favorite = String(isFavorite);
    const summary = projectUsage(usageByProject, dir, identity);
    const usage = el('span', 'group-usage', formatProjectUsage(summary));
    usage.title = formatProjectUsageTitle(summary);
    const plus = el('button', 'plus');
    plus.type = 'button';
    plus.title = '在 ' + startDir + ' 新建会话';
    plus.setAttribute('aria-label', '在「' + (leafOf(startDir) || '项目') + '」中新建会话');
    plus.innerHTML = [
      '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24"',
      ' fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"',
      ' stroke-linejoin="round"><path d="M6 22a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2',
      'h8a2.4 2.4 0 0 1 1.704.706l3.588 3.588A2.4 2.4 0 0 1 20 8v12a2 2',
      ' 0 0 1-2 2z"/><path d="M14 2v5a1 1 0 0 0 1 1h5"/><path d="M9 15h6"/>',
      '<path d="M12 18v-6"/></svg>',
    ].join('');
    plus.addEventListener('click', (event) => {
      event.stopPropagation();
      onStartNew(startDir);
    });
    head.addEventListener('click', () => onToggleGroup(identity, group, folder));
    head.addEventListener('contextmenu', (event) => {
      event.preventDefault();
      onContextMenu(event.clientX, event.clientY, {
        type: 'directory', dir: startDir, favorite: isFavorite,
      });
    });
    head.append(folder, name, usage, plus);
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
