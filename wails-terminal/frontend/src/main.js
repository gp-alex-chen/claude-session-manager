import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';
import './style.css';
import {
  GetAgents,
  GetOpenSessions,
  GetShell,
  ShellInstalled,
  SetShell,
  NotifyBeep,
  DebugLog,
  ListSessions,
  ListHiddenSessions,
  RenameSession,
  DeleteSession,
  UnhideSession,
  StartSession,
  StartNew,
  TermWrite,
  TermResize,
  TermKill,
} from '../../wailsjs/go/main/App';

// —— 多会话终端管理 ——
// 每个会话一个独立 xterm 实例；切换/关闭都在左侧列表操作，
// 后台会话继续运行并把输出写入自己（可能隐藏）的 Terminal。

const termStack = document.getElementById('terminal');
const statusEl = document.getElementById('status-bar');

// token -> { name, host, term, fit, exited, visible }
const sessions = new Map();
// 用户已关闭的 token：其后的迟到事件（最后帧输出/退出）一律丢弃，
// 避免关闭后又被 term:data 兜底逻辑重建出"幽灵标签"
const closedTokens = new Set();
// 会话 id -> 显示名（来自列表加载，兜底建档时查真实名字）
const sessionNames = new Map();
// 项目折叠状态（本次运行内有效）：dir -> collapsed
const collapsedDirs = new Set();
// 首屏是否已执行"默认全折叠"：只启动时折叠一次，之后保留用户手动折叠/展开
let collapseAllDone = false;
// 全局眼睛开关（仅折叠时有效）：false=睁眼（折叠时各组露出运行中的会话），
// true=闭眼（折叠即全隐藏）。展开时不影响显示。
let eyeGlobalOff = false;
let activeToken = null;
let newCounter = 0;
// 新建会话的"临时 token -> 真实会话 id"配对：
// StartNew 返回 new-<时间戳> 这类进程内临时 token，而 claude 稍后落盘的
// jsonl 对应真实会话 id。列表轮询刷新发现新 id 后在此配对，让行内
// × 关闭 / 点击重开 / 高亮都指向真实运行中的那个终端。
const realToNew = new Map(); // 真实 id -> new token（配对成功后才有）
const newToReal = new Map(); // new token -> 真实 id
let pendingNew = [];         // 等待配对的 { token, dir }

// —— 现成终端配色主题（xterm theme 对象；含社区知名配色） ——
const THEMES = {
  claude: {
    name: 'Claude 暖黑',
    foreground: '#e8e6e1', background: '#141412', cursor: '#d97757',
    cursorAccent: '#141412', selectionBackground: 'rgba(217,119,87,.32)', selectionForeground: '#ffffff',
    black: '#1f1e1b', red: '#e58a8a', green: '#8fb996', yellow: '#e0a458',
    blue: '#8ab4d8', magenta: '#c9a7d8', cyan: '#8ad0d0', white: '#e8e6e1',
    brightBlack: '#6f6c66', brightRed: '#f0a09a', brightGreen: '#a8ccae',
    brightYellow: '#eec07a', brightBlue: '#a3c8ea', brightMagenta: '#dcc0ea',
    brightCyan: '#a4e2e2', brightWhite: '#faf8f4',
  },
  dracula: {
    name: 'Dracula',
    foreground: '#f8f8f2', background: '#282a36', cursor: '#f8f8f2',
    cursorAccent: '#282a36', selectionBackground: 'rgba(189,147,249,.3)', selectionForeground: '#ffffff',
    black: '#21222c', red: '#ff5555', green: '#50fa7b', yellow: '#f1fa8c',
    blue: '#bd93f9', magenta: '#ff79c6', cyan: '#8be9fd', white: '#f8f8f2',
    brightBlack: '#6272a4', brightRed: '#ff6e6e', brightGreen: '#69ff94',
    brightYellow: '#ffffa5', brightBlue: '#d6acff', brightMagenta: '#ff92df',
    brightCyan: '#a4ffff', brightWhite: '#ffffff',
  },
  onedark: {
    name: 'One Dark',
    foreground: '#abb2bf', background: '#282c34', cursor: '#528bff',
    cursorAccent: '#282c34', selectionBackground: 'rgba(97,175,239,.3)', selectionForeground: '#ffffff',
    black: '#282c34', red: '#e06c75', green: '#98c379', yellow: '#e5c07b',
    blue: '#61afef', magenta: '#c678dd', cyan: '#56b6c2', white: '#abb2bf',
    brightBlack: '#5c6370', brightRed: '#be5046', brightGreen: '#98c379',
    brightYellow: '#d19a66', brightBlue: '#61afef', brightMagenta: '#c678dd',
    brightCyan: '#56b6c2', brightWhite: '#ffffff',
  },
  solarized: {
    name: 'Solarized Dark',
    foreground: '#839496', background: '#002b36', cursor: '#93a1a1',
    cursorAccent: '#002b36', selectionBackground: 'rgba(38,139,210,.3)', selectionForeground: '#ffffff',
    black: '#073642', red: '#dc322f', green: '#859900', yellow: '#b58900',
    blue: '#268bd2', magenta: '#d33682', cyan: '#2aa198', white: '#eee8d5',
    brightBlack: '#586e75', brightRed: '#cb4b16', brightGreen: '#859900',
    brightYellow: '#b58900', brightBlue: '#268bd2', brightMagenta: '#d33682',
    brightCyan: '#2aa198', brightWhite: '#fdf6e3',
  },
  nord: {
    name: 'Nord',
    foreground: '#d8dee9', background: '#2e3440', cursor: '#d8dee9',
    cursorAccent: '#2e3440', selectionBackground: 'rgba(136,192,208,.3)', selectionForeground: '#ffffff',
    black: '#3b4252', red: '#bf616a', green: '#a3be8c', yellow: '#ebcb8b',
    blue: '#81a1c1', magenta: '#b48ead', cyan: '#88c0d0', white: '#e5e9f0',
    brightBlack: '#4c566a', brightRed: '#bf616a', brightGreen: '#a3be8c',
    brightYellow: '#ebcb8b', brightBlue: '#81a1c1', brightMagenta: '#b48ead',
    brightCyan: '#8fbcbb', brightWhite: '#eceff4',
  },
  solarizedlight: {
    name: 'Solarized Light',
    foreground: '#657b83', background: '#fdf6e3', cursor: '#586e75',
    cursorAccent: '#fdf6e3', selectionBackground: 'rgba(38,139,210,.22)', selectionForeground: '#002b36',
    black: '#073642', red: '#dc322f', green: '#859900', yellow: '#b58900',
    blue: '#268bd2', magenta: '#d33682', cyan: '#2aa198', white: '#eee8d5',
    brightBlack: '#586e75', brightRed: '#cb4b16', brightGreen: '#859900',
    brightYellow: '#b58900', brightBlue: '#268bd2', brightMagenta: '#d33682',
    brightCyan: '#2aa198', brightWhite: '#fdf6e3',
  },
  onelight: {
    name: 'One Light',
    foreground: '#383a42', background: '#fafafa', cursor: '#526eff',
    cursorAccent: '#fafafa', selectionBackground: 'rgba(82,110,255,.2)', selectionForeground: '#383a42',
    black: '#383a42', red: '#e45649', green: '#50a14f', yellow: '#c18401',
    blue: '#0184bc', magenta: '#a626a4', cyan: '#0997b3', white: '#fafafa',
    brightBlack: '#a0a1a7', brightRed: '#e45649', brightGreen: '#50a14f',
    brightYellow: '#c18401', brightBlue: '#0184bc', brightMagenta: '#a626a4',
    brightCyan: '#0997b3', brightWhite: '#fafafa',
  },
  githublight: {
    name: 'GitHub Light',
    foreground: '#1f2328', background: '#ffffff', cursor: '#0969da',
    cursorAccent: '#ffffff', selectionBackground: 'rgba(9,105,218,.2)', selectionForeground: '#1f2328',
    black: '#24292f', red: '#cf222e', green: '#116329', yellow: '#4d2d00',
    blue: '#0969da', magenta: '#8250df', cyan: '#1b7c83', white: '#ffffff',
    brightBlack: '#6e7781', brightRed: '#cf222e', brightGreen: '#116329',
    brightYellow: '#4d2d00', brightBlue: '#0969da', brightMagenta: '#8250df',
    brightCyan: '#1b7c83', brightWhite: '#ffffff',
  },
};

const TERM_OPTS = {
  fontFamily: "'Cascadia Mono', Consolas, 'Microsoft YaHei', monospace",
  fontSize: 14,
  lineHeight: 1.2,
  cursorBlink: true,
  scrollback: 8000,
  theme: THEMES.claude,
};

// —— 日间/夜间 UI 模式（默认日间） ——
// 只影响外壳 UI（侧边栏/列表等）；终端配色由左下角设置菜单独立控制。
let uiTheme = 'light';
try {
  if (localStorage.getItem('ui-theme') === 'dark') uiTheme = 'dark';
} catch (e) { /* localStorage 不可用时保持默认日间 */ }
function applyUiTheme(mode) {
  uiTheme = mode;
  document.documentElement.dataset.theme = mode;
  try { localStorage.setItem('ui-theme', mode); } catch (e) {}
}
applyUiTheme(uiTheme);

// —— 工具 ——
function setStatus(msg, cls) {
  statusEl.innerHTML = '';
  statusEl.appendChild(document.createTextNode(msg));
  statusEl.className = cls || '';
}

function b64ToBytes(b64) {
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return bytes;
}

function bytesToB64(bytes) {
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}

// 写原始数据到指定会话的 PTY
function writeTerm(s, data) {
  TermWrite(s.token, bytesToB64(new TextEncoder().encode(data)));
}

// —— 系统剪贴板粘贴 ——
// 优先 navigator.clipboard（WebView2 的 http://wails.localhost 属安全上下文，
// 用户手势下可读）；失败时退回隐藏 textarea + execCommand('paste')。
function legacyReadClipboard() {
  const ta = document.createElement('textarea');
  ta.style.cssText = 'position:fixed;top:-9999px;left:-9999px;width:10px;height:10px;opacity:0;';
  document.body.appendChild(ta);
  ta.focus();
  let text = '';
  try {
    if (document.execCommand('paste')) text = ta.value;
  } catch (e) { /* 忽略 */ }
  ta.remove();
  return text;
}

async function pasteIntoTerm(s) {
  let text = '';
  try {
    text = await navigator.clipboard.readText();
  } catch (e) {
    text = legacyReadClipboard();
  }
  // 走 term.paste()：自动做 \r\n -> \r 转换；若 claude 开启了 bracketed paste
  // 模式则自动加 \x1b[200~..\x1b[201~ 包裹，多行内容不会立即触发提交
  if (text && s.term) s.term.paste(text);
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

// —— 会话终端（无标签栏：切换/关闭全在左侧列表操作） ——
function openTab(token, name) {
  closedTokens.delete(token); // 重新打开后此 token 的事件恢复有效
  let s = sessions.get(token);
  if (s) {
    activate(token);
    return s;
  }
  s = {
    token,
    name,
    labelText: name,
    exited: false,
    visible: false,
    term: null,
    fit: null,
  };

  s.host = el('div', 'term-host');
  termStack.appendChild(s.host);

  sessions.set(token, s);
  return s;
}

function makeTerminal(s) {
  const term = new Terminal(TERM_OPTS);
  const fit = new FitAddon();
  term.loadAddon(fit);
  // host 在后台时不可见，xterm 以默认尺寸工作，输出仍写入 buffer；
  // 切换到该会话时再 fit 校正尺寸。
  term.open(s.host);
  s.term = term;
  s.fit = fit;
  // 后台终端处于隐藏状态无法测量尺寸：先给个合理默认，
  // 激活时 fitAndSync 会校正并同步给后端
  if (!s.visible) term.resize(120, 32);

  // 前端输入：UTF-8 字节 -> base64 -> 后端写对应会话的 PTY
  term.onData((data) => writeTerm(s, data));

  // 显式接管组合键，避免被 WebView2 / xterm 默认行为吞掉：
  //  Ctrl+V / Ctrl+Shift+V / Shift+Insert -> 粘贴系统剪贴板
  //  Ctrl+Enter -> 发送换行(LF)，而不是当作回车提交
  term.attachCustomKeyEventHandler((e) => {
    if (e.type !== 'keydown') return true;
    const k = e.key.toLowerCase();
    if ((e.ctrlKey || e.metaKey) && k === 'v') {
      e.preventDefault();
      pasteIntoTerm(s);
      return false;
    }
    if (e.shiftKey && e.key === 'Insert') {
      e.preventDefault();
      pasteIntoTerm(s);
      return false;
    }
    if (e.ctrlKey && k === 'enter') {
      e.preventDefault();
      writeTerm(s, '\n');
      return false;
    }
    return true;
  });
  term.onResize(() => {
    if (s.visible) TermResize(s.token, term.cols, term.rows);
  });
  return term;
}

// 贴合尺寸并同步给后端。fit 后主动让出一列：xterm 的滚动条占据
// 视口右侧一列的空间，不让的话最后一列会被滚动条盖住/溢出。
function fitAndSync(s) {
  try {
    s.fit.fit();
    const cols = Math.max(2, s.term.cols - 1);
    if (cols !== s.term.cols) s.term.resize(cols, s.term.rows);
    TermResize(s.token, s.term.cols, s.term.rows);
  } catch (e) { /* 尺寸计算失败时忽略 */ }
}

function activate(token) {
  const s = sessions.get(token);
  if (!s) return;
  activeToken = token;
  for (const [t, e] of sessions) {
    const on = t === token;
    e.host.classList.toggle('active', on);
    e.visible = on;
  }
  syncActiveHighlight(); // 左侧列表高亮当前终端
  if (unreadSet.delete(token)) renderUnreadMarks(); // 查看过 = 清除未读
  if (!s.term) makeTerminal(s);
  fitAndSync(s);
  s.term.focus();
  setStatus('当前会话: ' + s.labelText + (s.exited ? '（已退出）' : ''), s.exited ? 'warn' : 'ok');
}

function disposeSession(token) {
  const s = sessions.get(token);
  if (!s) return;
  if (s.term) {
    try { s.term.dispose(); } catch (e) { /* ignore */ }
  }
  s.host.remove();
  sessions.delete(token);
}

// 左侧列表：高亮当前打开的终端对应的会话行
// （配对后的新会话：行 id 是真实 id，但终端 token 是 new-，需经映射）
function syncActiveHighlight() {
  for (const item of listEl.querySelectorAll('.session-item')) {
    const token = realToNew.get(item.dataset.id) || item.dataset.id;
    item.classList.toggle('active', token === activeToken);
  }
}

// 当前终端被 dispose 后：若无剩余终端则回到空态
function pickNextAfter(token) {
  const rest = [...sessions.keys()];
  if (activeToken === token) {
    if (rest.length) activate(rest[rest.length - 1]);
    else {
      activeToken = null;
      setStatus('未运行 — 点击左侧会话恢复，或点分组行 + 新建会话', '');
    }
  }
}

function closeTab(token) {
  const s = sessions.get(token);
  if (!s) {
    // 终端已不存在但仍可能留在待配对队列（new 启动失败等）：只清队列
    const i = pendingNew.findIndex(p => p.token === token);
    if (i >= 0) pendingNew.splice(i, 1);
    return;
  }
  closedTokens.add(token); // 先标记：迟到的数据/退出事件不再重建终端
  TermKill(token).catch(() => {});
  const real = newToReal.get(token); // 若曾配对到真实会话，解除映射
  if (real) { newToReal.delete(token); realToNew.delete(real); }
  const i2 = pendingNew.findIndex(p => p.token === token);
  if (i2 >= 0) pendingNew.splice(i2, 1);
  disposeSession(token);
  pickNextAfter(token);
}

window.addEventListener('resize', () => {
  const s = sessions.get(activeToken);
  if (s && s.term) fitAndSync(s);
});

// —— 终端配色主题切换（暗色：Claude 暖黑/Dracula/One Dark/Solarized/Nord；
//                       亮色：Solarized Light/One Light/GitHub Light） ——
let currentTheme = 'claude';
try {
  const saved = localStorage.getItem('term-theme');
  if (saved && THEMES[saved]) currentTheme = saved;
} catch (e) { /* localStorage 不可用时保持默认 */ }
TERM_OPTS.theme = THEMES[currentTheme] || THEMES.claude;
// 同步终端宿主背景（CSS --term-bg），否则边框/滚动条缝隙处仍透出暗色
document.documentElement.style.setProperty('--term-bg', TERM_OPTS.theme.background);

function applyTheme(name) {
  currentTheme = name;
  try { localStorage.setItem('term-theme', name); } catch (e) {}
  const t = THEMES[name] || THEMES.claude;
  TERM_OPTS.theme = t;
  document.documentElement.style.setProperty('--term-bg', t.background);
  for (const [, s] of sessions) {
    if (s.term) s.term.options.theme = t; // 已打开的所有终端即时换肤
  }
  setStatus('终端配色已切换: ' + t.name, 'ok');
}

// —— 左下角设置菜单：界面外观（日间/夜间）+ 终端配色（8 套） ——
const settingsBtn = document.getElementById('btn-settings');
const settingsMenu = document.getElementById('settings-menu');
function hideSettingsMenu() { settingsMenu.style.display = 'none'; }
function buildSettingsMenu() {
  settingsMenu.innerHTML = '';
  const label1 = el('div', 'settings-group-label', '界面外观');
  settingsMenu.appendChild(label1);
  for (const [mode, text] of [['light', '☀️ 日间模式'], ['dark', '🌙 夜间模式']]) {
    const it = el('div', 'settings-item' + (mode === uiTheme ? ' cur' : ''), text);
    it.dataset.mode = mode;
    it.addEventListener('click', () => { applyUiTheme(mode); hideSettingsMenu(); });
    settingsMenu.appendChild(it);
  }
  const label2 = el('div', 'settings-group-label', '终端配色');
  settingsMenu.appendChild(label2);
  for (const key of Object.keys(THEMES)) {
    const it = el('div', 'settings-item' + (key === currentTheme ? ' cur' : ''), THEMES[key].name);
    it.dataset.theme = key;
    it.style.setProperty('--dot', THEMES[key].background);
    it.addEventListener('click', () => { applyTheme(key); hideSettingsMenu(); });
    settingsMenu.appendChild(it);
  }
  // 底层 Shell：启动 claude 用的终端外壳（只影响之后新启动/恢复的会话）
  appendShellGroup();
}

// 底层 Shell 选项单独构建（每次打开菜单都重新拉一次当前值）
async function appendShellGroup() {
  let shell = 'cmd';
  try { shell = await GetShell(); } catch (e) { /* 保持默认 cmd */ }
  const label3 = el('div', 'settings-group-label', '底层 Shell');
  settingsMenu.appendChild(label3);
  for (const [key, text] of [['cmd', 'cmd.exe（默认）'], ['pwsh', 'pwsh（PowerShell 7）']]) {
    const it = el('div', 'settings-item' + (key === shell ? ' cur' : ''), text);
    it.dataset.shell = key;
    it.title = key === 'pwsh'
      ? '需要已安装 PowerShell 7（pwsh 在 PATH 中）；claude 退出后停留在 pwsh 提示符'
      : 'Windows 自带；claude 退出后终端随之结束';
    it.addEventListener('click', async () => {
      // 选型时校验：pwsh 未安装则拒绝切换，避免"选完打不开"
      if (key === 'pwsh') {
        let ok = false;
        try { ok = await ShellInstalled('pwsh'); } catch (e) { /* 视为未安装 */ }
        if (!ok) {
          setStatus('未检测到 pwsh（PowerShell 7）：请先安装并确保 pwsh 在 PATH 中，或保持 cmd', 'warn');
          return;
        }
      }
      try {
        await SetShell(key);
      } catch (e) {
        setStatus('切换 Shell 失败: ' + e, 'warn');
        return;
      }
      hideSettingsMenu();
      setStatus('底层 Shell 已切换: ' + (key === 'pwsh' ? 'pwsh' : 'cmd') + '（新启动/恢复的会话生效）', 'ok');
    });
    settingsMenu.appendChild(it);
  }
  // 若已选择 pwsh 但当前系统检测不到：提示当前会以 cmd 兜底启动
  if (shell === 'pwsh') {
    let ok = true;
    try { ok = await ShellInstalled('pwsh'); } catch (e) { ok = false; }
    if (!ok) {
      settingsMenu.appendChild(el('div', 'settings-note', '⚠ 当前系统未检测到 pwsh，新启动的会话将以 cmd 兜底，装好后自动恢复 pwsh'));
    }
  }
}
settingsBtn.addEventListener('click', (ev) => {
  ev.stopPropagation();
  const willOpen = settingsMenu.style.display === 'none';
  hideSettingsMenu();
  if (!willOpen) return;
  buildSettingsMenu(); // 每次打开重建，确保 cur 标记为最新选择
  const r = settingsBtn.getBoundingClientRect();
  // 按钮位于窗口左下角，菜单向上展开
  settingsMenu.style.left = r.left + 'px';
  settingsMenu.style.bottom = (window.innerHeight - r.top + 4) + 'px';
  settingsMenu.style.display = 'block';
});
document.addEventListener('click', hideSettingsMenu);
window.addEventListener('blur', hideSettingsMenu);

// —— 右键菜单（重命名 / 归档） ——
const ctxMenu = document.createElement('div');
ctxMenu.id = 'ctx-menu';
document.body.appendChild(ctxMenu);
let ctxTarget = null;

function addCtxItem(label, fn, danger) {
  const d = el('div', 'ctx-item' + (danger ? ' danger' : ''), label);
  d.addEventListener('click', () => { hideCtx(); fn(); });
  ctxMenu.appendChild(d);
}

function showCtx(x, y, target) {
  ctxTarget = target;
  ctxMenu.innerHTML = '';
  if (target.type === 'session') {
    addCtxItem('重命名…', () => renameSession(target));
    addCtxItem('归档（不再显示）', () => deleteSession(target), true);
  }
  ctxMenu.style.left = Math.min(x, window.innerWidth - 160) + 'px';
  ctxMenu.style.top = Math.min(y, window.innerHeight - 140) + 'px';
  ctxMenu.style.display = 'block';
}

function hideCtx() { ctxMenu.style.display = 'none'; ctxTarget = null; }
document.addEventListener('click', hideCtx);
window.addEventListener('blur', hideCtx);

// 重命名：本地别名（不动 claude 数据；留空恢复原名；真正改名请用 claude 内 /rename）
async function renameSession(target) {
  const cur = sessionNames.get(target.id) || target.name;
  const input = window.prompt('重命名会话（留空 = 恢复原名）\n提示：这里只改本软件的显示名，真正改名请在 claude 会话内使用 /rename', cur);
  if (input === null) return;
  const name = input.trim();
  try {
    await RenameSession(target.id, name);
  } catch (e) {
    setStatus('重命名失败: ' + e, 'warn');
    return;
  }
  sessionNames.set(target.id, name || target.name);
  // 同步更新已打开终端的状态栏名字
  const ss = sessions.get(target.id);
  if (ss) ss.labelText = name || target.name;
  await loadSessions();
  setStatus(name ? '已重命名: ' + name : '已恢复原名', 'ok');
}

// 归档：软隐藏（不物理删除会话文件，可随时在"归档"面板恢复）
async function deleteSession(target) {
  if (!window.confirm('归档会话（不再显示）？\n会话文件不会被删除，可随时在顶部「归档」中恢复。')) return;
  try {
    await DeleteSession(target.id);
  } catch (e) {
    setStatus('归档失败: ' + e, 'warn');
    return;
  }
  closedTokens.add(target.id); // 关闭其终端并丢弃迟到事件
  closeTab(target.id);
  sessionNames.delete(target.id);
  await loadSessions();
  setStatus('已归档（不再显示）: ' + target.name, 'warn');
}

// —— 归档（软隐藏）面板 ——
const hiddenPanel = document.getElementById('hidden-panel');
const hiddenCountEl = document.getElementById('hidden-count');
let hiddenOpen = false;

async function refreshHidden(silent) {
  let list = [];
  try {
    list = await ListHiddenSessions();
  } catch (e) {
    if (!silent) setStatus('加载归档失败: ' + e, 'warn');
    return;
  }
  hiddenCountEl.textContent = list.length;
  if (!hiddenOpen) return;
  hiddenPanel.innerHTML = '';
  if (!list.length) {
    hiddenPanel.appendChild(el('div', 'hidden-item', '（无归档会话）'));
    return;
  }
  for (const s of list) {
    const row = el('div', 'hidden-item');
    const nm = el('span', 'h-name', s.name);
    nm.title = s.dir;
    const dir = el('span', 'h-dir', leafOf(s.dir));
    const btn = el('button', '', '恢复');
    btn.title = '在列表中重新显示此会话';
    btn.addEventListener('click', async () => {
      try {
        await UnhideSession(s.id);
      } catch (e) {
        setStatus('恢复失败: ' + e, 'warn');
        return;
      }
      await loadSessions();
      setStatus('已恢复显示: ' + s.name, 'ok');
    });
    row.appendChild(nm);
    row.appendChild(dir);
    row.appendChild(btn);
    hiddenPanel.appendChild(row);
  }
}

document.getElementById('btn-hidden').addEventListener('click', () => {
  hiddenOpen = !hiddenOpen;
  hiddenPanel.classList.toggle('hidden', !hiddenOpen);
  if (hiddenOpen) refreshHidden(true);
});

// —— 运行状态徽标（claude agents --json，每 10s 轮询） ——
// 语义（2026-08-18 实测 state/status 字段）：
//   正在执行任务: state=working / status=busy      -> 绿色心电图跳动
//   交互会话(打开但空闲): kind=interactive 非 busy -> 绿色静态 ◉
//   等待权限批准: state=blocked / status=waiting   -> 橙色 ⚠ 闪烁（需要用户处理）
//   后台待命/排队: kind=background 非 busy          -> 橙色 ●
//   已完成: state=done                             -> 灰点 + 未读逻辑接管
//   不在列表:                                      -> 灰点（未运行）
function classifyAgent(id) {
  const a = runningMap.get(id);
  if (!a) return 'idle';
  if (a.state === 'done') return 'idle'; // 已完成：归入未运行视觉，未读逻辑接管
  if (a.status === 'busy' || a.state === 'working' || a.state === 'queued') return 'working';
  if (a.state === 'blocked' || a.status === 'waiting') return 'blocked';
  if (a.kind === 'interactive') return 'open';
  return 'bg';
}

// —— 实时状态：后端常驻监视器推送 agents:update 事件 ——
// 后端起 goroutine 每 1~2s 拉一次 claude agents 快照，变化时推事件。
// 前端不再轮询：收到事件立即应用。30s 兜底轮询防止事件丢失。
// 一次只处理一份数据（事件到达即同步执行，无并发叠加问题）。
async function refreshAgents() {
  let list = [];
  try {
    list = await GetAgents(); // 读后端缓存，秒回
  } catch (e) {
    return;
  }
  applyAgents(list);
}

function applyAgents(list) {
  const next = new Map();
  for (const a of list) if (a.sessionId) next.set(a.sessionId, a);
  DebugLog('应用 agents=' + list.length + ' 条, 可识别' + next.size + ' 个');

  // 完成检测：会话"从亮到灭/从忙到闲"就算结束，两种语义都支持：
  //   background: state working -> done（或消失）
  //   interactive: status busy -> idle（任务做完回到待输入，实测无 state 字段）
  for (const [id, prev] of prevRunning) {
    const cur = next.get(id);
    const prevBusy = prev.status === 'busy' || prev.state === 'working' || prev.state === 'queued';
    const curBusy = !!(cur && (cur.status === 'busy' || cur.state === 'working' || cur.state === 'queued'));
    // 结束 = 消失 / state=done / 上一轮忙碌而本轮不再忙碌
    const nowEnded = !cur || cur.state === 'done' || (prevBusy && !curBusy && cur);
    const skip = closedTokens.has(id); // 用户手动关闭的会话不提示
    DebugLog(`检测 id=${id} prev=${prev.state}/${prev.status} cur=${cur ? cur.state + '/' + cur.status : 'gone'} prevBusy=${prevBusy} curBusy=${curBusy} ended=${nowEnded} 提示过=${endedSet.has(id)} 关闭=${skip}`);
    if (nowEnded && !endedSet.has(id) && !skip) {
      endedSet.add(id); // 一次结束只提示一次
      DebugLog(`>>> 触发提示 ${id} 类型=${prevBusy ? '任务完成' : '会话结束'}`);
      markEnded(id, sessionNames.get(id) || id, prevBusy);
    }
  }
  prevRunning = new Map(next);
  runningMap.clear();
  for (const [id, a] of next) runningMap.set(id, a);

  // 会话重新忙碌（又开新任务）则允许再次提示
  for (const id of endedSet) {
    const a = runningMap.get(id);
    if (a && (a.status === 'busy' || a.state === 'working' || a.state === 'queued')) endedSet.delete(id);
  }

  renderUnreadMarks();

  refreshFoldState(); // 状态变化：变忙浮现、变闲收回
  const badges = document.querySelectorAll('#session-list .badge');
  for (const b of badges) {
    if (b.classList.contains('ended-anim')) continue; // 正在播结束动画，跳过
    const id = b.dataset.id;
    if (unreadSet.has(id)) continue; // 未读标记优先（替换状态点，查看后换回）
    const cls = classifyAgent(id);
    b.classList.remove('ecg', 'open', 'running', 'blocked', 'idle');
    if (cls === 'working') {
      b.classList.add('ecg', 'green');
      b.textContent = ECG[ecgTick];
      b.title = '正在执行任务';
    } else if (cls === 'open') {
      b.classList.add('open');
      b.textContent = '◉';
      b.title = '已打开（交互会话，空闲等待输入）';
    } else if (cls === 'blocked') {
      b.classList.add('blocked');
      b.textContent = '⚠';
      b.title = '等待权限批准';
    } else if (cls === 'bg') {
      b.classList.add('running');
      b.textContent = '●';
      b.title = '后台待命/排队';
    } else {
      b.classList.add('idle');
      b.textContent = '●';
      b.title = '未运行';
    }
  }
}

// 实时事件推送（后端监视器检测到状态变化立即发来）+ 30s 兜底轮询
window.runtime.EventsOn('agents:update', (list) => applyAgents(list || []));
setInterval(() => GetAgents().then(applyAgents).catch(() => {}), 30000);
refreshAgents();

// —— 后端事件路由 ——
window.runtime.EventsOn('term:data', (token, b64) => {
  if (closedTokens.has(token)) return; // 已关闭的会话：丢弃迟到输出
  let s = sessions.get(token);
  if (!s) {
    // 兜底：数据先于终端到达时自动建档（名字尽量用列表里的真实会话名）
    s = openTab(token, sessionNames.get(token) || '正在连接…');
  }
  if (!s.term) makeTerminal(s);
  s.term.write(b64ToBytes(b64));
});

window.runtime.EventsOn('term:exit', (token) => {
  if (closedTokens.has(token)) return; // 已关闭的会话：忽略退出事件
  const real = newToReal.get(token);
  if (real) {
    // 曾配对到真实会话的"新会话"终端退出：解除映射并清理临时终端，
    // 真实会话行保留（agents 徽标回到未运行，可点击重新打开）
    newToReal.delete(token);
    realToNew.delete(real);
    disposeSession(token);
    pickNextAfter(token);
    return;
  }
  if (token.startsWith('new-')) {
    // 从未配对的新终端退出（如 claude 启动即失败）：移出待配队列并清理
    const i = pendingNew.findIndex(p => p.token === token);
    if (i >= 0) pendingNew.splice(i, 1);
    disposeSession(token);
    pickNextAfter(token);
    return;
  }
  const s = sessions.get(token);
  if (!s) return;
  s.exited = true;
  setStatus('会话已退出: ' + s.labelText, 'warn');
});

// 眼睛开关图标（SVG，随当前颜色渲染）
const ICON_EYE_OPEN = '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12Z"/><circle cx="12" cy="12" r="2.8"/></svg>';
const ICON_EYE_CLOSED = '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><path d="M4 10.2C6 8.4 8.8 7.4 12 7.4s6 1 8 2.8"/><path d="M4 13.8c2 1.8 4.8 2.8 8 2.8s6-1 8-2.8"/><path d="M6.5 5.5l11 13"/></svg>';

function setEyeIcon(eye, off) {
  eye.innerHTML = off ? ICON_EYE_CLOSED : ICON_EYE_OPEN;
}

// —— 会话列表 ——
const listEl = document.getElementById('session-list');
// 最近一次全量列表 + 运行中信息（id -> kind），用于渲染状态徽标
let lastLoaded = [];
const runningMap = new Map();
const unreadSet = new Set(); // 已结束且未查看的会话
const endedSet = new Set();   // 已完成并提示过（防重复提示）的会话
let prevRunning = new Map(); // 上一次轮询的运行中集合（检测"刚结束"）

// —— 心电图心跳节拍（共享 tick：所有运行中徽标同步跳动） ——
const ECG = ['▁', '▂', '▃', '▅', '▇', '▅', '▃', '▂', '▁', '─', '─', '─'];
let ecgTick = 0;
setInterval(() => {
  ecgTick = (ecgTick + 1) % ECG.length;
  for (const e of document.querySelectorAll('.badge.ecg')) {
    if (!e.classList.contains('ended-anim')) e.textContent = ECG[ecgTick];
  }
}, 120);

// 顶部横幅提示（任务完成等），2.8s 后自动淡出
function showToast(text) {
  let t = document.getElementById('toast');
  if (!t) {
    t = el('div', 'toast');
    t.id = 'toast';
    document.body.appendChild(t);
  }
  t.textContent = text;
  t.classList.add('show');
  clearTimeout(t._tm);
  t._tm = setTimeout(() => t.classList.remove('show'), 2800);
}

// 按会话真实状态重绘某个徽标（动画结束后恢复正确样式，
// 避免把还活着的交互会话错标成"未运行"灰色）
function repaintBadge(b) {
  const id = b.dataset.id;
  const cls = classifyAgent(id);
  b.classList.remove('ecg', 'open', 'running', 'blocked', 'idle', 'ended-anim', 'unread');
  if (cls === 'working') {
    b.classList.add('ecg', 'green');
    b.textContent = ECG[ecgTick];
    b.title = '正在执行任务';
  } else if (cls === 'open') {
    b.classList.add('open');
    b.textContent = '◉';
    b.title = '已打开（交互会话，空闲等待输入）';
  } else if (cls === 'blocked') {
    b.classList.add('blocked');
    b.textContent = '⚠';
    b.title = '等待权限批准';
  } else if (cls === 'bg') {
    b.classList.add('running');
    b.textContent = '●';
    b.title = '后台待命/排队';
  } else {
    b.classList.add('idle');
    b.textContent = '●';
    b.title = '未运行';
  }
}

// 任务/会话结束提示：心跳停止动画（置顶区会整块重建，动画贴在列表区徽标上）+
// 顶部横幅 + 提示音 + 未读标记（正在看该会话时不打扰）。
// wasWorking=true 提示"任务完成"，false（交互空闲会话结束）提示"会话结束"。
function markEnded(id, name, wasWorking) {
  const watching = activeToken === id; // 用户正开着这个会话的终端
  if (!watching) unreadSet.add(id);

  const item = listEl.querySelector('.group .session-item[data-id="' + id + '"]');
  const badge = item && item.querySelector('.badge');
  if (badge && !badge.classList.contains('ended-anim')) {
    badge.classList.add('ended-anim');
    badge.textContent = '─';
    // 动画结束后摘掉 ended-anim（否则残留类会让徽标停在淡出态），
    // 再交给 renderUnreadMarks 决定显示：未读点 or 原状态标记
    const b = badge;
    setTimeout(() => {
      b.classList.remove('ended-anim');
      renderUnreadMarks();
    }, 900);
  }

  const label = wasWorking ? '任务完成' : '会话结束';
  showToast('✅ ' + label + '：' + name);
  setStatus(label + ': ' + name, 'ok');
  NotifyBeep().catch(() => {});
  renderUnreadMarks();
}

// 按 unreadSet 切换会话左侧徽标：
//   未读 -> 用橙色脉冲点"替换"原状态标记；查看/点击后 -> 换回原状态标记
// （ended-anim 动画播放中的徽标不动，动画结束后由 markEnded 的回调收尾）
function renderUnreadMarks() {
  for (const item of listEl.querySelectorAll('.session-item')) {
    const id = item.dataset.id;
    const badge = item.querySelector('.badge');
    if (!badge) continue;
    if (unreadSet.has(id)) {
      if (badge.classList.contains('ended-anim')) continue; // 动画中：播完再变未读点
      if (!badge.classList.contains('unread')) {
        badge.classList.remove('ecg', 'open', 'running', 'blocked', 'idle');
        badge.classList.add('unread');
        badge.textContent = '●';
        badge.title = '已完成 · 未读（点击查看）';
      }
    } else if (badge.classList.contains('unread') || badge.classList.contains('ended-anim')) {
      repaintBadge(badge); // 点击查看后换回原状态标记（含动画残留的徽标）
    }
  }
}

async function openFromList(s) {
  const token = realToNew.get(s.id) || s.id; // 已配对的新会话：切到其运行中的终端
  const existing = sessions.get(token);
  if (existing && !existing.exited) {
    activate(token); // 已在运行：直接换过去
    unreadSet.delete(s.id); // 查看过 = 清除未读
    renderUnreadMarks();
    return;
  }
  if (existing) {
    // 已退出：重建终端后重启
    disposeSession(token);
  }
  openTab(token, s.name);
  try {
    await StartSession(s.id, s.dir);
    setStatus('已恢复: ' + s.name, 'ok');
    unreadSet.delete(s.id); // 查看过 = 清除未读
    renderUnreadMarks();
    activate(token);
  } catch (e) {
    setStatus('恢复失败: ' + e, 'warn');
    disposeSession(token);
  }
}

// 折叠联动：
//   展开的组        -> 全量显示（眼睛状态不影响）
//   折叠 + 睁眼     -> 只显示"打开中"的会话（classifyAgent != idle）
//   折叠 + 闭眼     -> 全部隐藏（纯折叠）
function refreshFoldState() {
  for (const item of listEl.querySelectorAll('.group .session-item')) {
    const id = item.dataset.id;
    const dir = item.dataset.dir;
    const cls = classifyAgent(id);
    const folded = collapsedDirs.has(dir) && (eyeGlobalOff || cls === 'idle');
    item.classList.toggle('fold-hidden', folded);
  }
}

// 会话列表签名（id+目录+名字）：轮询比对用。不含时间列——
// 运行中的会话持续写 transcript 会让 mtime 一直变，若把时间算进签名
// 会导致每轮都重建整树；名字/新增/删失才值得重建。
function listSig(list) {
  return list.map(s => [s.id, s.dir, s.name].join('|')).join('\n');
}

// 全量刷新：拉取 + 重建左侧列表（启动 / 重命名 / 归档等调用）
async function loadSessions() {
  let list;
  try {
    list = await ListSessions();
  } catch (e) {
    setStatus('加载会话失败: ' + e, 'warn');
    return;
  }
  renderSessions(list);
}

// 尝试把列表中新出现的真实会话 id 配对给等待中的 new 终端。
// 规则：同目录、FIFO（先启动的 new 先配对到先出现的真实 id）；
// 只在真实 id 首次出现的那一轮执行（此后在 lastLoaded 里，不会再算作新出现）。
function pairNewSessions(list) {
  if (!pendingNew.length) return;
  const prev = new Set(lastLoaded.map(x => x.id));
  const newInDir = new Map(); // dir -> [id, ...]（出现顺序）
  for (const s of list) {
    if (prev.has(s.id) || realToNew.has(s.id)) continue;
    if (!newInDir.has(s.dir)) newInDir.set(s.dir, []);
    newInDir.get(s.dir).push(s.id);
  }
  const stillPending = [];
  for (const p of pendingNew) {
    const ids = newInDir.get(p.dir);
    const id = ids && ids.length ? ids.shift() : null;
    if (!id) { stillPending.push(p); continue; }
    newToReal.set(p.token, id);
    realToNew.set(id, p.token);
    const s = sessions.get(p.token);
    if (s) {
      const info = list.find(x => x.id === id);
      if (info) s.labelText = info.name; // 状态栏名字顺带更新为真实名
    }
    if (activeToken === p.token) syncActiveHighlight(); // 高亮跟着真实行走
  }
  pendingNew = stillPending;
}

// 后台轮询刷新：新建会话 claude 要稍后才落盘 jsonl，claude 内 /rename、
// 删失等也都是异步的——每 5s 比对一次签名，有变化才重建列表。
let lastListSig = null;
async function autoRefreshSessions() {
  let list;
  try {
    list = await ListSessions();
  } catch (e) {
    return;
  }
  const sig = listSig(list);
  if (sig === lastListSig) return; // 无变化，跳过整树重建
  pairNewSessions(list); // 先配对（配对基于"上一轮"的 lastLoaded 判定新 id）
  renderSessions(list);
}
setInterval(autoRefreshSessions, 5000);

function renderSessions(list) {
  lastLoaded = list;
  lastListSig = listSig(list);
  // 软件打开首屏：默认闭合所有目录树（只执行一次；此后用户手动折叠/展开）
  if (!collapseAllDone && list.length) {
    collapseAllDone = true;
    for (const s of list) collapsedDirs.add(s.dir);
  }
  const groups = new Map();
  for (const s of list) {
    sessionNames.set(s.id, s.name);
    if (!groups.has(s.dir)) groups.set(s.dir, []);
    groups.get(s.dir).push(s);
  }
  listEl.innerHTML = '';
  for (const [dir, items] of groups) {
    const g = el('div', 'group');
    if (collapsedDirs.has(dir)) g.classList.add('collapsed');
    const head = el('div', 'group-head');
    const chev = el('span', 'chevron');
    chev.title = '点击折叠/展开';
    const name = el('span', 'group-name', leafOf(dir));
    name.title = dir;
    const plus = el('button', 'plus', '+');
    plus.title = '在 ' + dir + ' 新建会话';
    plus.addEventListener('click', async (ev) => {
      ev.stopPropagation();
      try {
        const token = await StartNew(dir);
        const label = '新会话 ' + (++newCounter) + ' · ' + leafOf(dir);
        openTab(token, label);
        sessions.get(token).dir = dir; // 记住所属目录，供轮询配对新会话 id
        pendingNew.push({ token, dir });
        activate(token);
        setStatus('已启动新会话: ' + leafOf(dir), 'ok');
      } catch (e) {
        setStatus('新建失败: ' + e, 'warn');
      }
    });
    // 点击组头 = 折叠/展开整个项目；立即重算可见性，不等下一轮轮询
    head.addEventListener('click', () => {
      const collapsed = g.classList.toggle('collapsed');
      if (collapsed) collapsedDirs.add(dir);
      else collapsedDirs.delete(dir);
      chev.classList.toggle('collapsed', collapsed);
      refreshFoldState();
    });
    head.appendChild(chev);
    head.appendChild(name);
    head.appendChild(plus);
    g.appendChild(head);

    const body = el('div', 'group-body');
    for (const s of items) {
      const item = el('div', 'session-item');
      item.dataset.id = s.id;
      item.dataset.dir = s.dir;
      item.title = s.dir;
      // 折叠的组：闭眼 -> 全隐藏；睁眼 -> 只隐藏未运行（灰点）的
      if (collapsedDirs.has(dir) &&
          (eyeGlobalOff || classifyAgent(s.id) === 'idle')) {
        item.classList.add('fold-hidden');
      }
      const nameRow = el('div', 's-name');
      const badge = el('span', 'badge idle', '●');
      badge.dataset.id = s.id;
      badge.title = '未运行';
      nameRow.appendChild(badge);
      nameRow.appendChild(el('span', 's-name-text', s.name));
      // 关闭按钮在会话行右侧（hover 显示）：结束进程并关闭终端。
      // 配对的"新会话"需经映射关到真正的 new token 才会生效
      const closeBtn = el('span', 's-close', '×');
      closeBtn.title = '关闭此终端（结束进程）';
      closeBtn.addEventListener('click', (ev) => {
        ev.stopPropagation();
        closeTab(realToNew.get(s.id) || s.id);
      });
      nameRow.appendChild(closeBtn);
      item.appendChild(nameRow);
      const timeRow = el('div', 's-time', s.time);
      item.appendChild(timeRow);
      item.addEventListener('click', () => openFromList(s));
      // 右键菜单：重命名 / 归档
      item.addEventListener('contextmenu', (ev) => {
        ev.preventDefault();
        showCtx(ev.clientX, ev.clientY, { type: 'session', id: s.id, dir: s.dir, name: s.name });
      });
      body.appendChild(item);
    }
    g.appendChild(body);
    listEl.appendChild(g);
  }
  refreshHidden(true); // 每次刷新后同步"归档"计数
  refreshAgents();     // 列表重建后立即刷新状态徽标
  refreshFoldState();
  renderUnreadMarks();
  syncActiveHighlight(); // 列表重建后恢复当前终端的高亮
}

// 全局眼睛开关按钮（仅折叠时有效；展开时显示不受影响）
const btnEye = document.getElementById('btn-eye');
function paintEye() {
  btnEye.classList.toggle('off', eyeGlobalOff);
  btnEye.title = eyeGlobalOff
    ? '折叠时隐藏所有会话（点击开启：折叠时显示运行中的）'
    : '折叠时显示运行中的会话（点击关闭：折叠即全部隐藏）';
  setEyeIcon(btnEye, eyeGlobalOff);
}
btnEye.addEventListener('click', () => {
  eyeGlobalOff = !eyeGlobalOff;
  paintEye();
  refreshFoldState();
});
paintEye();

(async () => {
  await refreshAgents(); // 先取一轮 agents：首屏折叠/徽标不依赖轮询迟到
  await loadSessions();
  // 恢复上次关闭时还打开着的所有会话（已被归档/已从磁盘消失的自动跳过；
  // 逐个走 openFromList，行为与手动点击一致）
  try {
    const open = await GetOpenSessions();
    if (Array.isArray(open) && open.length) {
      const byId = new Map(lastLoaded.map(s => [s.id, s]));
      for (const id of open) {
        const s = byId.get(id);
        if (s) await openFromList(s);
      }
    }
  } catch (e) { /* 恢复失败不影响主界面 */ }
})();