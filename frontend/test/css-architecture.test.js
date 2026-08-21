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
  const dynamic = new Set(['dot', 'term-bg', 'theme-bg', 'theme-fg']);
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
  assert.match(menus, /settings-nav button:focus-visible/);
  assert.match(menus, /settings-theme-card:focus-visible/);
  assert.match(menus, /settings-shell-card:focus-visible/);
  assert.match(menus, /update-action:focus-visible/);
});

test('settings motion uses corporate timing and reduced-motion fallback', () => {
  const menus = read('menus.css');
  assert.match(menus, /cubic-bezier\(\.2,\s*0,\s*0,\s*1\)/);
  assert.match(menus, /280ms/);
  assert.match(menus, /180ms/);
  assert.match(menus, /90ms/);
  assert.match(menus, /#settings-menu\.settings-overlay:not\(\[hidden\]\)/);
  assert.match(menus, /prefers-reduced-motion:\s*reduce/);
  assert.match(menus, /transition:\s*none/);
});

test('settings layout adapts and update progress includes a track', () => {
  const menus = read('menus.css');
  assert.match(menus, /@media\s*\(max-width:\s*640px\)/);
  assert.match(menus, /@media\s*\(max-width:\s*480px\)/);
  assert.match(menus, /\.settings-nav[\s\S]*flex-direction:\s*row/);
  assert.match(menus, /\.settings-progress-region|\.update-progress-region[\s\S]*background:\s*var\(--panel-3\)/);
  assert.match(menus, /\.update-progress-bar[\s\S]*background:\s*var\(--accent\)/);
});

test('obsolete settings menu selectors are gone from production sources', () => {
  const sourceDir = path.resolve(stylesDir, '..');
  const production = fs.readdirSync(sourceDir, { recursive: true })
    .filter((name) => typeof name === 'string' && /\.(css|js|html)$/.test(name))
    .map((name) => fs.readFileSync(path.join(sourceDir, name), 'utf8'))
    .join('\n');
  assert.doesNotMatch(production, /settings-item|settings-group-label|upd-(run|col|progress|hint|note-ver)/);
});
