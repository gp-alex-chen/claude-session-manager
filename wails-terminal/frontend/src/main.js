import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';
import './style.css';
import {
  ListSessions,
  StartSession,
  StartNew,
  TermWrite,
  TermResize,
} from '../../wailsjs/go/main/App';

// —— 终端 ——
const term = new Terminal({
  fontFamily: "'Cascadia Mono', Consolas, 'Microsoft YaHei', monospace",
  fontSize: 14,
  lineHeight: 1.15,
  cursorBlink: true,
  scrollback: 8000,
  theme: {
    background: '#141414',
    foreground: '#e0e0e0',
    cursor: '#ffd700',
    selectionBackground: '#3a3a5a',
  },
});
const fit = new FitAddon();
term.loadAddon(fit);

const termDiv = document.getElementById('terminal');
term.open(termDiv);

function fitTerm() {
  fit.fit();
  TermResize(term.cols, term.rows);
}
window.addEventListener('resize', fitTerm);
setTimeout(fitTerm, 50);

// 后端输出：base64 -> bytes -> term.write（避免 JSON 破坏二进制/半截 UTF-8）
window.runtime.EventsOn('term:data', (b64) => {
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  term.write(bytes);
});

window.runtime.EventsOn('term:exit', () => {
  setStatus('会话已退出 — 点击左侧会话重新开始', 'warn');
});

// 前端输入：UTF-8 字节 -> base64 -> 后端写 PTY
term.onData((data) => {
  const bytes = new TextEncoder().encode(data);
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  TermWrite(btoa(bin));
});
term.onResize(() => TermResize(term.cols, term.rows));

// —— 会话列表 ——
const listEl = document.getElementById('session-list');
const statusEl = document.getElementById('status-bar');

function setStatus(msg, cls) {
  statusEl.innerHTML = '';
  statusEl.appendChild(document.createTextNode(msg));
  statusEl.className = cls || '';
}

function leafOf(dir) {
  const parts = dir.split(/[\\/]/).filter(Boolean);
  return parts.length ? parts[parts.length - 1] : dir;
}

function el(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
}

async function loadSessions() {
  let sessions;
  try {
    sessions = await ListSessions();
  } catch (e) {
    setStatus('加载会话失败: ' + e, 'warn');
    return;
  }
  // 按目录分组
  const groups = new Map();
  for (const s of sessions) {
    if (!groups.has(s.dir)) groups.set(s.dir, []);
    groups.get(s.dir).push(s);
  }
  listEl.innerHTML = '';
  for (const [dir, list] of groups) {
    const g = el('div', 'group');
    const head = el('div', 'group-head');
    const name = el('span', 'group-name', leafOf(dir));
    name.title = dir;
    const plus = el('button', 'plus', '+');
    plus.title = '在 ' + dir + ' 新建会话';
    plus.addEventListener('click', async (ev) => {
      ev.stopPropagation();
      try {
        await StartNew(dir);
        setStatus('已启动新会话: ' + leafOf(dir), 'ok');
        term.focus();
      } catch (e) {
        setStatus('新建失败: ' + e, 'warn');
      }
    });
    head.appendChild(name);
    head.appendChild(plus);
    g.appendChild(head);

    const body = el('div', 'group-body');
    for (const s of list) {
      const item = el('div', 'session-item');
      item.title = s.dir;
      item.appendChild(el('div', 's-name', s.name));
      item.appendChild(el('div', 's-time', s.time));
      item.addEventListener('click', async () => {
        try {
          await StartSession(s.id, s.dir);
          setStatus('已恢复: ' + s.name, 'ok');
          term.focus();
        } catch (e) {
          setStatus('恢复失败: ' + e, 'warn');
        }
      });
      body.appendChild(item);
    }
    g.appendChild(body);
    listEl.appendChild(g);
  }
}

document.getElementById('btn-refresh').addEventListener('click', loadSessions);
loadSessions();
