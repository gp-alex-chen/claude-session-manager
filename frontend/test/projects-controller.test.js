import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const sourceDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../src');
const indexHTML = fs.readFileSync(path.join(sourceDir, 'index.html'), 'utf8');
const sidebarCSS = fs.readFileSync(path.join(sourceDir, 'styles/sidebar.css'), 'utf8');

test('project bar contains only the project label and one add button', () => {
  const match = indexHTML.match(/<section id="project-bar"[\s\S]*?<\/section>/);
  assert.ok(match, 'project bar must exist');
  const projectBar = match[0];

  assert.match(projectBar, />项目</);
  assert.match(projectBar, /<button id="btn-add-project"[^>]*>\s*\+\s*<\/button>/);
  assert.equal((projectBar.match(/<button\b/g) || []).length, 1);
  for (const forbidden of [
    'project-list', 'project-item', 'project-name', 'project-plus', 'project-delete',
  ]) {
    assert.doesNotMatch(projectBar, new RegExp(forbidden));
  }
});

test('project bar is positioned above the existing session list', () => {
  const projectBarIndex = indexHTML.indexOf('id="project-bar"');
  const sessionListIndex = indexHTML.indexOf('id="session-list"');
  assert.ok(projectBarIndex >= 0);
  assert.ok(sessionListIndex > projectBarIndex);
});

test('project bar uses the sidebar panel background instead of panel-2', () => {
  const block = sidebarCSS.match(/#project-bar\s*\{([\s\S]*?)\}/)?.[1] || '';

  assert.doesNotMatch(block, /background:\s*var\(--panel-2\)/);
  assert.match(block, /background:\s*(?:var\(--panel\)|transparent)/);
  for (const forbidden of [
    '#project-list', '.project-item', '.project-name', '.project-plus',
    '.project-delete', '.project-empty',
  ]) {
    assert.equal(sidebarCSS.includes(forbidden), false, forbidden);
  }
});
