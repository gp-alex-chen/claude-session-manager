import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const sourceDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../src');
const indexHTML = fs.readFileSync(path.join(sourceDir, 'index.html'), 'utf8');
const sidebarCSS = fs.readFileSync(path.join(sourceDir, 'styles/sidebar.css'), 'utf8');

test('project bar groups the eye filter and add-project controls', () => {
  const match = indexHTML.match(/<section id="project-bar"[\s\S]*?<\/section>/);
  assert.ok(match, 'project bar must exist');
  const projectBar = match[0];

  assert.match(projectBar, />项目</);
  assert.match(projectBar, /<button id="btn-eye" type="button"[^>]*><\/button>/);
  assert.match(projectBar, /<button id="btn-add-project"[^>]*>\s*\+\s*<\/button>/);
  assert.equal((projectBar.match(/<button\b/g) || []).length, 2);
  assert.ok(projectBar.indexOf('id="btn-eye"') < projectBar.indexOf('id="btn-add-project"'));
  for (const forbidden of [
    'project-list', 'project-item', 'project-name', 'project-plus', 'project-delete',
  ]) {
    assert.doesNotMatch(projectBar, new RegExp(forbidden));
  }
});

test('sidebar header keeps the eye filter out of the archive controls', () => {
  const match = indexHTML.match(/<div id="sidebar-header">[\s\S]*?<\/div>\s*<div id="hidden-panel"/);
  assert.ok(match, 'sidebar header must exist');
  assert.doesNotMatch(match[0], /id="btn-eye"/);
  assert.match(match[0], /id="btn-hidden"/);
});

test('sidebar titles are not selectable', () => {
  assert.match(sidebarCSS, /\.brand,\s*\.project-bar-title\s*\{[^}]*user-select:\s*none/);
});

test('project controls reveal on hover and keyboard focus', () => {
  assert.match(sidebarCSS, /#project-bar:hover\s+\.project-bar-actions/);
  assert.match(sidebarCSS, /#project-bar:focus-within\s+\.project-bar-actions/);
  assert.match(sidebarCSS, /\.project-bar-actions\s*\{[^}]*opacity:\s*0/);
  assert.match(sidebarCSS, /\.project-bar-actions\s*\{[^}]*pointer-events:\s*none/);
});

test('group new-session controls reveal on hover and keyboard focus', () => {
  assert.match(sidebarCSS, /\.plus\s*\{[^}]*opacity:\s*0/);
  assert.match(sidebarCSS, /\.group-head:hover\s+\.plus/);
  assert.match(sidebarCSS, /\.group-head:focus-within\s+\.plus/);
  assert.match(sidebarCSS, /\.plus\s*\{[^}]*pointer-events:\s*none/);
});

test('group session folding uses a transitionable hidden state', () => {
  assert.match(sidebarCSS, /\.session-item\s*\{[^}]*max-height:\s*72px/);
  assert.match(sidebarCSS, /\.session-item\s*\{[^}]*transition:[^}]*max-height/);
  assert.match(sidebarCSS, /\.group\.collapsed\s+\.group-body\s+\.session-item\.fold-hidden\s*\{[^}]*max-height:\s*0/);
  assert.match(sidebarCSS, /\.group\.collapsed\s+\.group-body\s+\.session-item\.fold-hidden\s*\{[^}]*opacity:\s*0/);
  assert.match(sidebarCSS, /\.group\.collapsed\s+\.group-body\s+\.session-item\.fold-hidden\s*\{[^}]*pointer-events:\s*none/);
  assert.match(sidebarCSS, /@media\s*\(prefers-reduced-motion:\s*reduce\)[\s\S]*\.session-item\s*\{?\s*transition:\s*none/);
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
