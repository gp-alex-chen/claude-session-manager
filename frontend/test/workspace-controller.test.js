import test from 'node:test';
import assert from 'node:assert/strict';
import { createWorkspaceController } from '../src/workspace/controller.js';
import { createPreferenceStorage } from '../src/settings/storage.js';
import { createAppState } from '../src/state/app-state.js';

function node() {
  const listeners = new Map();
  const classes = new Set();
  const attributes = new Map();
  const styles = new Map();
  return { listeners, attributes, styles, hidden: false, textContent: '',
    style: { setProperty: (key, value) => styles.set(key, value) },
    classList: { add: (...names) => names.forEach(n => classes.add(n)),
      remove: (...names) => names.forEach(n => classes.delete(n)),
      toggle: (name, value) => value ? classes.add(name) : classes.delete(name) },
    setAttribute: (key, value) => attributes.set(key, String(value)),
    addEventListener: (name, callback) => listeners.set(name, callback),
    removeEventListener: (name, callback) => { if (listeners.get(name) === callback) listeners.delete(name); },
    emit: (name, values = {}) => listeners.get(name)?.({ preventDefault() {}, ...values }),
  };
}

function fixture(storage) {
  const nodes = Object.fromEntries(['app','sidebar','sidebar-divider','btn-sidebar-toggle',
    'workspace-project','workspace-directory','workspace-summary','status-message'].map(key=>[key,node()]));
  const documentRef = node();
  const state = createAppState();
  const timers = new Map();
  let resizes = 0;
  let timerId = 0;
  const controller = createWorkspaceController({ state, nodes, documentRef, storage,
    onResize: () => { resizes++; }, setTimeoutFn: cb => { timers.set(++timerId,cb); return timerId; },
    clearTimeoutFn: id => timers.delete(id) });
  controller.initialize();controller.start();
  return {controller,state,nodes,documentRef,timers,get resizes(){return resizes;}};
}

test('sidebar geometry survives shutdown, preference persistence and a fresh renderer', async () => {
  const saved = {};
  const backend = { LoadPreferences: async () => ({...saved}), SetPreference: async (k,v) => { saved[k]=v; } };
  const firstStorage = createPreferenceStorage({backend});await firstStorage.initialize();
  const first = fixture(firstStorage);
  first.state.terminals.set('one',{dir:'C:/work/project',exited:false});
  first.controller.setWidth(310);first.controller.toggleSidebar();
  assert.equal(first.state.terminals.size,1);
  first.controller.stop();await firstStorage.flush();
  const secondStorage = createPreferenceStorage({backend});await secondStorage.initialize();
  const second = fixture(secondStorage);
  assert.equal(second.nodes.app.styles.get('--sidebar-width'),'310px');
  assert.equal(second.nodes.sidebar.hidden,true);
  second.nodes['btn-sidebar-toggle'].emit('click');
  assert.equal(second.nodes.sidebar.hidden,false);
  assert.equal(second.nodes['sidebar-divider'].hidden,false);
});

test('sidebar drag clamps dimensions and persists once when the owning pointer releases', () => {
  const writes=[];
  const f=fixture({getItem:()=>null,setItem:(k,v)=>writes.push([k,v])});
  const divider=f.nodes['sidebar-divider'];
  assert.equal(divider.attributes.get('aria-valuenow'),'280');
  divider.emit('pointerdown',{button:0,pointerId:7,clientX:280});
  f.documentRef.emit('pointermove',{pointerId:8,clientX:900});
  assert.equal(f.nodes.app.styles.get('--sidebar-width'),'280px');
  f.documentRef.emit('pointermove',{pointerId:7,clientX:900});
  assert.equal(f.nodes.app.styles.get('--sidebar-width'),'360px');
  assert.deepEqual(writes,[]);
  f.documentRef.emit('pointerup',{pointerId:8});
  assert.deepEqual(writes,[]);
  f.documentRef.emit('pointerup',{pointerId:7});
  assert.deepEqual(writes,[['sidebar-width','360']]);
  assert.equal(f.documentRef.listeners.size,0);
  divider.emit('keydown',{key:'Home'});
  assert.equal(divider.attributes.get('aria-valuenow'),'220');
  assert.ok(f.resizes>=2);
});

test('toolbar follows focused session paths and counts only live terminals', () => {
  const f=fixture();
  f.state.terminals.set('one',{dir:'C:/work/alpha',exited:false});
  f.state.terminals.set('two',{dir:'C:/work/beta',exited:false});
  f.state.terminals.set('gone',{exited:true});
  f.state.activeToken='two';f.controller.refreshContext();
  assert.equal(f.nodes['workspace-project'].textContent,'beta');
  assert.equal(f.nodes['workspace-directory'].textContent,'C:/work/beta');
  assert.equal(f.nodes['workspace-summary'].textContent,'2 个会话已打开');
});

test('success timeout cannot clear a later warning and stop releases timers and drag listeners', () => {
  const f=fixture();
  f.controller.setStatus('Saved','ok');
  assert.equal(f.timers.size,1);
  f.controller.setStatus('Cannot save','warn');
  assert.equal(f.timers.size,0);
  assert.equal(f.nodes['status-message'].textContent,'Cannot save');
  f.nodes['sidebar-divider'].emit('pointerdown',{pointerId:1,clientX:250});
  f.controller.setStatus('Done','ok');f.controller.stop();
  assert.equal(f.documentRef.listeners.size,0);
  assert.equal(f.timers.size,0);
  assert.equal(f.nodes['btn-sidebar-toggle'].listeners.size,0);
  f.controller.start();f.nodes['btn-sidebar-toggle'].emit('click');
  assert.equal(f.nodes.sidebar.hidden,true);
});
