import test from 'node:test';
import assert from 'node:assert/strict';

import {
  LAYOUT_MODES,
  PANE_IDS,
  allPresets,
  dividerVisibility,
  getPreset,
  isValidLayoutMode,
  layoutAfterPaneClear,
  paneFillOrder,
  normalizeLayoutMode,
  visiblePaneIds,
} from '../src/panes/presets.js';

test('fixed layout presets expose the four requested split modes', () => {
  assert.deepEqual(LAYOUT_MODES, [
    'split-rows-2', 'split-cols-2', 'split-main-left-3', 'grid-2x2',
  ]);
  assert.deepEqual(PANE_IDS, ['pane-0', 'pane-1', 'pane-2', 'pane-3']);
  assert.deepEqual(visiblePaneIds('split-rows-2'), ['pane-0', 'pane-1']);
  assert.deepEqual(visiblePaneIds('split-cols-2'), ['pane-0', 'pane-1']);
  assert.deepEqual(visiblePaneIds('split-main-left-3'), ['pane-0', 'pane-1', 'pane-2']);
  assert.deepEqual(visiblePaneIds('grid-2x2'), PANE_IDS);
});

test('layout descriptors have stable labels and invalid values fall back to single', () => {
  assert.equal(isValidLayoutMode('single'), true);
  assert.equal(isValidLayoutMode('grid-2x2'), true);
  assert.equal(isValidLayoutMode('drag-anywhere'), false);
  assert.equal(normalizeLayoutMode('drag-anywhere'), 'single');
  assert.equal(getPreset('split-main-left-3').label, '三分屏');
  assert.equal(getPreset('invalid').mode, 'single');
  assert.deepEqual(allPresets().map((preset) => preset.mode), [
    'single', 'split-rows-2', 'split-cols-2', 'split-main-left-3', 'grid-2x2',
  ]);
});

test('layout descriptors own the divider visibility for every layout', () => {
  assert.deepEqual(dividerVisibility('single'), { vertical: false, horizontal: false });
  assert.deepEqual(dividerVisibility('split-rows-2'), { vertical: false, horizontal: true });
  assert.deepEqual(dividerVisibility('split-cols-2'), { vertical: true, horizontal: false });
  assert.deepEqual(dividerVisibility('split-main-left-3'), { vertical: true, horizontal: true });
  assert.deepEqual(dividerVisibility('grid-2x2'), { vertical: true, horizontal: true });
  assert.deepEqual(dividerVisibility('invalid'), { vertical: false, horizontal: false });
});

test('four-pane layout fills the top and bottom positions before the second column', () => {
  assert.deepEqual(paneFillOrder('single'), ['pane-0']);
  assert.deepEqual(paneFillOrder('split-main-left-3'), ['pane-0', 'pane-1', 'pane-2']);
  assert.deepEqual(paneFillOrder('grid-2x2'), ['pane-0', 'pane-2', 'pane-1', 'pane-3']);
  assert.deepEqual(paneFillOrder('invalid'), ['pane-0']);
});

test('pane clear transitions cover every supported layout and pane position', () => {
  for (const paneId of ['pane-0', 'pane-1']) {
    assert.equal(layoutAfterPaneClear('split-rows-2', paneId), 'single');
    assert.equal(layoutAfterPaneClear('split-cols-2', paneId), 'single');
  }
  assert.equal(layoutAfterPaneClear('split-main-left-3', 'pane-0'), 'split-rows-2');
  assert.equal(layoutAfterPaneClear('split-main-left-3', 'pane-1'), 'split-cols-2');
  assert.equal(layoutAfterPaneClear('split-main-left-3', 'pane-2'), 'split-cols-2');
  for (const paneId of ['pane-0', 'pane-1', 'pane-2', 'pane-3']) {
    assert.equal(layoutAfterPaneClear('grid-2x2', paneId), 'split-main-left-3');
  }
  assert.equal(layoutAfterPaneClear('single', 'pane-0'), 'single');
  assert.equal(layoutAfterPaneClear('grid-2x2', 'unknown'), 'grid-2x2');
});
