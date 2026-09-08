import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const stylesDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../src/styles');
const sourceDir = path.resolve(stylesDir, '..');
const read = (name) => fs.readFileSync(path.join(stylesDir, name), 'utf8');
const splitBlock = (source, selector) => {
  const match = source.match(new RegExp(selector + '\\s*\\{([\\s\\S]*?)\\}'));
  return match ? match[1] : '';
};

test('style entry imports tokens first, base second, and terminal last', () => {
  const entry = read('style.css');
  const imports = [...entry.matchAll(/@import\s+['"]([^'"]+)['"]/g)].map((match) => match[1]);
  assert.deepEqual(imports, ['./themes.css', './base.css', './sidebar.css', './menus.css', './terminal.css']);
  for (const imported of imports) assert.equal(fs.existsSync(path.join(stylesDir, imported)), true);
  assert.equal(entry.replace(/@import\s+['"][^'"]+['"]\s*;/g, '').replace(/\/\*[\s\S]*?\*\//g, '').trim(), '');
  for (const file of fs.readdirSync(stylesDir).filter((name) => name !== 'style.css')) {
    assert.equal(read(file).includes('@import'), false, file + ' must not import CSS');
  }
});

test('themes define unique dark/light tokens without the old placeholder', () => {
  const themes = read('themes.css');
  assert.equal(themes.includes('--panel-radius'), false);
  const root = splitBlock(themes, ':root');
  const light = splitBlock(themes, 'html\\[data-theme="light"\\]');
  const names = (block) => [...block.matchAll(/--([\w-]+)\s*:/g)].map((match) => match[1]);
  const rootNames = names(root);
  const lightNames = names(light);
  assert.equal(new Set(rootNames).size, rootNames.length);
  assert.equal(new Set(lightNames).size, lightNames.length);
  const colorTokens = rootNames.filter((name) => name !== 'radius' && name !== 'radius-sm');
  assert.deepEqual(new Set(lightNames), new Set(colorTokens));
});

test('all CSS variable references have a token declaration or dynamic ownership', () => {
  const declared = new Set([
    ...read('themes.css').matchAll(/--([\w-]+)\s*:/g),
  ].map((match) => match[1]));
  const dynamic = new Set([
    'dot', 'term-bg', 'theme-bg', 'theme-fg',
    'terminal-split-columns-first', 'terminal-split-columns-second',
    'terminal-split-rows-first', 'terminal-split-rows-second',
  ]);
  for (const file of fs.readdirSync(stylesDir).filter((name) => name.endsWith('.css'))) {
    const source = read(file);
    for (const [, name] of source.matchAll(/var\(--([\w-]+)/g)) {
      assert.equal(declared.has(name) || dynamic.has(name), true, `${file}: --${name}`);
    }
  }
});

test('controller-driven classes and states remain represented in split styles', () => {
  const css = fs.readdirSync(stylesDir)
    .filter((name) => name.endsWith('.css'))
    .map(read)
    .join('\n');
  for (const selector of [
    'active', 'fold-hidden', 'collapsed', 'off', 'idle', 'open', 'running',
    'blocked', 'ecg', 'green', 'ended-anim', 'unread', 'show', 'cur',
    'has-update', 'disabled', 'hidden', 'ok', 'warn',
  ]) {
    assert.match(css, new RegExp('\\.' + selector + '\\b|#status-bar\\.' + selector));
  }
});

test('terminal styles define adjustable pane geometries and divider tracks', () => {
  const terminal = read('terminal.css');
  const terminalPaneBlock = splitBlock(terminal, '#terminal-pane');
  const terminalBlock = splitBlock(terminal, '#terminal');
  assert.match(terminalPaneBlock, /min-height:\s*0/);
  assert.match(terminalBlock, /min-height:\s*0/);
  const terminalHostBlock = splitBlock(terminal, '\\.term-host');
  assert.match(terminalHostBlock, /inset:\s*10px 2px 10px 10px/);
  assert.match(terminalHostBlock, /border:\s*0/);
  assert.match(terminal, /#terminal \.terminal-pane-body \.term-host\s*\{\s*position:\s*absolute;\s*inset:\s*10px 2px 10px 10px;/);
  assert.match(terminal, /\.term-host \.xterm \.xterm-viewport\s*\{[\s\S]*?scrollbar-width:\s*none/);
  assert.match(terminal, /\.term-host \.xterm \.xterm-viewport::-webkit-scrollbar\s*\{[\s\S]*?width:\s*0/);
  for (const mode of ['single', 'split-rows-2', 'split-cols-2', 'split-main-left-3', 'grid-2x2']) {
    assert.match(terminal, new RegExp(`#terminal\\[data-layout-mode="${mode}"\\]`), mode);
  }
  const menuBlock = terminal.match(/\.terminal-layout-menu\s*\{([\s\S]*?)\}/)?.[1] || '';
  assert.match(menuBlock, /top:\s*calc\(100% \+ 7px\)/);
  assert.match(menuBlock, /bottom:\s*auto/);
  assert.match(terminal, /#terminal\[data-layout-mode="split-main-left-3"\] \[data-pane-id="pane-0"\]\s*\{\s*grid-area:\s*1 \/ 1 \/ 4 \/ 2;/);
  assert.match(terminal, /#terminal\[data-layout-mode="split-main-left-3"\] \[data-pane-id="pane-1"\]\s*\{\s*grid-area:\s*1 \/ 3;/);
  assert.match(terminal, /#terminal\[data-layout-mode="split-main-left-3"\] \[data-pane-id="pane-2"\]\s*\{\s*grid-area:\s*3 \/ 3;/);
  assert.match(terminal, /#terminal\[data-layout-mode="grid-2x2"\] \[data-pane-id="pane-3"\]\s*\{\s*grid-area:\s*3 \/ 3;/);
  assert.match(terminal, /split-main-left-3[^}]*[\s\S]*pane-0/);
  assert.match(terminal, /terminal-pane-body \.term-host\.is-mounted/);
  assert.match(terminal, /#terminal\.terminal-layout[\s\S]*background:\s*var\(--bg\)/);
  assert.match(terminal, /#terminal\.terminal-layout[\s\S]*gap:\s*0/);
  assert.match(terminal, /terminal-pane-divider-vertical[\s\S]*cursor:\s*col-resize/);
  assert.match(terminal, /terminal-pane-divider-horizontal[\s\S]*cursor:\s*row-resize/);
  assert.match(terminal, /terminal-pane-divider:hover::before/);
  assert.match(terminal, /terminal-pane-divider\.is-dragging::before/);
  assert.match(terminal, /--terminal-split-columns-first/);
  assert.match(terminal, /--terminal-split-rows-first/);
  assert.match(terminal, /terminal-pane-slot[\s\S]*border:\s*1px solid transparent/);
  assert.match(terminal, /terminal-pane-slot::after[\s\S]*z-index:\s*10/);
  assert.match(terminal, /terminal-pane-slot::after[\s\S]*pointer-events:\s*none/);
  assert.match(terminal, /terminal-pane-slot\.is-focused::after\s*\{\s*border-color:\s*var\(--accent\)/);
  assert.match(terminal, /terminal-pane-slot\.is-focused \.terminal-pane-header[\s\S]*background:\s*var\(--accent-soft\)/);
  assert.match(terminal, /terminal-pane-slot\.is-focused \.terminal-pane-header[\s\S]*box-shadow:\s*inset 3px 0 0 var\(--accent\)/);
  assert.match(terminal, /terminal-pane-current[\s\S]*visibility:\s*hidden/);
  assert.match(terminal, /terminal-pane-current[\s\S]*width:\s*32px/);
  assert.match(terminal, /terminal-pane-slot\.is-focused \.terminal-pane-current[\s\S]*visibility:\s*visible/);
  assert.match(terminal, /terminal-pane-usage-summary[\s\S]*width:\s*88px/);
  assert.match(terminal, /terminal-pane-usage-summary[\s\S]*flex:\s*0 0 88px/);
  assert.match(terminal, /terminal-pane-usage-details[\s\S]*position:\s*absolute/);
  assert.doesNotMatch(terminal, /terminal-pane-title::after/);
});

test('complete selectors are owned by one CSS module', () => {
  const ownership = new Map();
  for (const file of fs.readdirSync(stylesDir).filter((name) => name.endsWith('.css'))) {
    const source = read(file).replace(/\/\*[\s\S]*?\*\//g, '');
    for (const block of source.split('}')) {
      const selector = block.split('{')[0].trim();
      if (!selector || selector.startsWith('@')) continue;
      if (!ownership.has(selector)) ownership.set(selector, file);
      else assert.equal(ownership.get(selector), file, selector);
    }
  }
});

test('settings skeleton provides an overlay, dialog, navigation, and panel surface', () => {
  const menus = read('menus.css');
  const html = fs.readFileSync(path.join(sourceDir, 'index.html'), 'utf8');
  assert.match(menus, /#settings-menu\.settings-overlay/);
  assert.match(menus, /\.settings-dialog/);
  assert.match(menus, /\.settings-nav/);
  assert.match(menus, /\.settings-panel/);
  assert.match(html, /id="settings-nav"[^>]*role="tablist"/);
  for (const category of ['appearance', 'terminal', 'update']) {
    assert.match(html, new RegExp(`id="settings-tab-${category}"[\\s\\S]*?role="tab"`));
    assert.match(html, new RegExp(`id="settings-tab-${category}"[\\s\\S]*?aria-controls="settings-panel-${category}"`));
  }
});

test('update panel has dedicated card styles', () => {
  const menus = read('menus.css');
  for (const selector of ['.update-card', '.update-action', '.update-progress-region', '.update-warning']) {
    assert.match(menus, new RegExp('\\' + selector));
  }
});

test('settings dialog has stable sizing, hierarchy, and focused controls', () => {
  const menus = read('menus.css');
  assert.match(menus, /width:\s*min\(680px/);
  assert.match(menus, /height:\s*min\(500px/);
  assert.match(menus, /max-height:\s*min\(500px/);
  assert.match(menus, /settings-version[\s\S]*background:/);
  assert.match(menus, /#settings-close[\s\S]*width:\s*38px/);
  assert.match(menus, /#settings-close[\s\S]*height:\s*38px/);
  assert.match(menus, /settings-nav button:focus-visible/);
  assert.match(menus, /settings-theme-card:focus-visible/);
  assert.match(menus, /settings-shell-card:focus-visible/);
  assert.match(menus, /settings-font-size-button:focus-visible/);
  assert.match(menus, /update-action:focus-visible/);
});

test('settings motion uses corporate timing and reduced-motion fallback', () => {
  const menus = read('menus.css');
  assert.match(menus, /cubic-bezier\(\.2,\s*0,\s*0,\s*1\)/);
  assert.match(menus, /280ms/);
  assert.match(menus, /180ms/);
  assert.match(menus, /90ms/);
  assert.match(menus, /#settings-menu\.settings-overlay:not\(\[hidden\]\)/);
  assert.match(menus, /@keyframes\s+settings-overlay-in/);
  assert.match(menus, /@keyframes\s+settings-dialog-in/);
  assert.match(menus, /@keyframes\s+settings-panel-in/);
  assert.match(menus, /settings-overlay-in[\s\S]*280ms/);
  assert.match(menus, /settings-dialog-in[\s\S]*280ms/);
  assert.match(menus, /settings-panel-in[\s\S]*180ms/);
  assert.match(menus, /settings-segment:active/);
  assert.match(menus, /settings-theme-card:active/);
  assert.match(menus, /settings-shell-card:active/);
  assert.match(menus, /settings-font-size-button:active/);
  assert.match(menus, /update-action:active/);
  assert.match(menus, /settings-nav button:active/);
  assert.match(menus, /prefers-reduced-motion:\s*reduce/);
  assert.match(menus, /transition:\s*none/);
  assert.match(menus, /animation:\s*none/);
});

test('settings layout adapts and update progress includes a track', () => {
  const menus = read('menus.css');
  assert.match(menus, /@media\s*\(max-width:\s*640px\)/);
  assert.match(menus, /@media\s*\(max-width:\s*480px\)/);
  assert.match(menus, /\.settings-nav[\s\S]*flex-direction:\s*row/);
  assert.match(menus, /\.settings-progress-region|\.update-progress-region[\s\S]*background:\s*var\(--panel-3\)/);
  assert.match(menus, /\.update-progress-bar[\s\S]*background:\s*var\(--accent\)/);
  assert.match(menus, /\.settings-font-size-control[\s\S]*display:\s*flex/);
  assert.match(menus, /\.settings-font-size-value/);
  assert.match(menus, /\.settings-font-size-button:disabled/);
  assert.match(menus, /@media\s*\(max-width:\s*480px\)[\s\S]*settings-font-size-control/);
});

test('token usage surface owns accessible popover, compact groups, and responsive motion rules', () => {
  const terminal = read('terminal.css');
  const sidebar = read('sidebar.css');
  assert.match(terminal, /#status-message/);
  assert.match(terminal, /\.terminal-pane-usage-details[^{}]*\{[\s\S]*?transition:[^;]*180ms[^;]*cubic-bezier\(\.2,0,0,1\)/);
  assert.match(terminal, /\.terminal-pane-usage-details[\s\S]*?max-height:\s*calc\(100% - 41px\)/);
  assert.match(terminal, /terminal-pane-usage-summary[\s\S]*?border-radius:\s*var\(--radius-sm\)[\s\S]*?background:/);
  assert.match(terminal, /\.usage-hero-grid[\s\S]*?display:\s*grid/);
  assert.match(terminal, /\.usage-compare-table/);
  assert.match(terminal, /\.usage-request-grid[\s\S]*?grid-template-columns/);
  assert.match(terminal, /@keyframes\s+usage-details-in[\s\S]*?opacity:\s*0[\s\S]*?transform:[\s\S]*?opacity:\s*1/);
  assert.match(terminal, /\.terminal-pane-usage-details:not\(\[hidden\]\)[\s\S]*?animation:\s*usage-details-in\s+180ms/);
  assert.match(terminal, /@media\s*\(prefers-reduced-motion:\s*reduce\)[\s\S]*?transition:\s*none[\s\S]*?animation:\s*none/);
  assert.match(sidebar, /\.group-usage/);
  assert.doesNotMatch(terminal, /#status-bar\.(ok|warn)/);
});

test('token usage is pane-local and no longer mounted in the top status bar', () => {
  const terminal = read('terminal.css');
  const html = fs.readFileSync(path.join(sourceDir, 'index.html'), 'utf8');
  assert.match(terminal, /terminal-pane-usage-summary/);
  assert.match(terminal, /terminal-pane-usage-details/);
  assert.doesNotMatch(terminal, /#usage-summary/);
  assert.doesNotMatch(terminal, /#usage-details/);
  assert.doesNotMatch(html, /id="usage-summary"/);
  assert.doesNotMatch(html, /id="usage-details"/);
  assert.match(html, /id="status-message"/);
});

test('obsolete settings menu selectors are gone from production sources', () => {
  const sourceDir = path.resolve(stylesDir, '..');
  const production = fs.readdirSync(sourceDir, { recursive: true })
    .filter((name) => typeof name === 'string' && /\.(css|js|html)$/.test(name))
    .map((name) => fs.readFileSync(path.join(sourceDir, name), 'utf8'))
    .join('\n');
  assert.doesNotMatch(production, /settings-item|settings-group-label|upd-(run|col|progress|hint|note-ver)/);
});
