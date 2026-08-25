import test from 'node:test';
import assert from 'node:assert/strict';

import { renderProjectBar } from '../src/sessions/view.js';

class FakeNode {
  constructor() {
    this.children = [];
    this.className = '';
    this.listeners = new Map();
    this.textContent = '';
    this.title = '';
    this.type = '';
    this._innerHTML = '';
  }
  append(...children) { this.children.push(...children); }
  appendChild(child) { this.children.push(child); return child; }
  addEventListener(name, callback) { this.listeners.set(name, callback); }
  click() { return this.listeners.get('click')?.({ stopPropagation() {} }); }
  set innerHTML(value) {
    this._innerHTML = value;
    if (value === '') this.children = [];
  }
  get innerHTML() { return this._innerHTML; }
  querySelector(selector) {
    const matches = (node) => selector.startsWith('.')
      && node.className.split(/\s+/).includes(selector.slice(1));
    const visit = (node) => {
      for (const child of node.children) {
        if (matches(child)) return child;
        const nested = visit(child);
        if (nested) return nested;
      }
      return null;
    };
    return visit(this);
  }
  querySelectorAll(selector) {
    const matches = [];
    const visit = (node) => {
      for (const child of node.children) {
        if (selector.startsWith('.') && child.className.split(/\s+/).includes(selector.slice(1))) {
          matches.push(child);
        }
        visit(child);
      }
    };
    visit(this);
    return matches;
  }
}

function makeFixture() {
  const root = new FakeNode();
  const el = (tag, className, text) => {
    const node = new FakeNode();
    node.tagName = tag.toUpperCase();
    node.className = className || '';
    node.textContent = text || '';
    return node;
  };
  return { root, el };
}

test('renderProjectBar shows leaf names, full-path titles, and project plus buttons', () => {
  const fixture = makeFixture();
  const dir = 'C:\\work\\alpha';

  renderProjectBar({ listRoot: fixture.root, projects: [dir], el: fixture.el });

  const name = fixture.root.querySelector('.project-name');
  assert.ok(name);
  assert.equal(name.textContent, 'alpha');
  assert.equal(name.title, dir);
  const plus = fixture.root.querySelector('.project-plus');
  assert.ok(plus);
  assert.equal(plus.textContent, '+');
  assert.equal(plus.type, 'button');
});

test('renderProjectBar safely renders an empty project list', () => {
  const fixture = makeFixture();

  renderProjectBar({ listRoot: fixture.root, projects: [], el: fixture.el });

  assert.equal(fixture.root.querySelectorAll('.project-item').length, 0);
  assert.equal(fixture.root.children.length, 1);
  assert.equal(fixture.root.children[0].className, 'project-empty');
});

test('renderProjectBar forwards a project plus click to onStartNew', async () => {
  const fixture = makeFixture();
  const dir = 'C:\\work\\alpha';
  const started = [];

  renderProjectBar({
    listRoot: fixture.root,
    projects: [dir],
    el: fixture.el,
    onStartNew: (projectDir) => started.push(projectDir),
  });

  await fixture.root.querySelector('.project-plus').click();
  assert.deepEqual(started, [dir]);
});

test('renderProjectBar keeps plus before delete and forwards delete click', async () => {
  const fixture = makeFixture();
  const dir = 'C:\\work\\alpha';
  const deleted = [];

  renderProjectBar({
    listRoot: fixture.root,
    projects: [dir],
    el: fixture.el,
    onDeleteProject: (projectDir) => deleted.push(projectDir),
  });

  const item = fixture.root.children[0];
  assert.deepEqual(item.children.map((child) => child.className), [
    'project-name', 'project-plus', 'project-delete',
  ]);
  await item.children[2].click();
  assert.deepEqual(deleted, [dir]);
});
