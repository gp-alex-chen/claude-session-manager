export const PANE_IDS = Object.freeze(['pane-0', 'pane-1', 'pane-2', 'pane-3']);

export const LAYOUT_MODES = Object.freeze([
  'split-rows-2',
  'split-cols-2',
  'split-main-left-3',
  'grid-2x2',
]);

const PRESETS = Object.freeze({
  single: Object.freeze({
    mode: 'single',
    label: '单屏',
    visiblePaneIds: Object.freeze(['pane-0']),
  }),
  'split-rows-2': Object.freeze({
    mode: 'split-rows-2',
    label: '上下双屏',
    visiblePaneIds: Object.freeze(['pane-0', 'pane-1']),
  }),
  'split-cols-2': Object.freeze({
    mode: 'split-cols-2',
    label: '左右双屏',
    visiblePaneIds: Object.freeze(['pane-0', 'pane-1']),
  }),
  'split-main-left-3': Object.freeze({
    mode: 'split-main-left-3',
    label: '三分屏',
    visiblePaneIds: Object.freeze(['pane-0', 'pane-1', 'pane-2']),
  }),
  'grid-2x2': Object.freeze({
    mode: 'grid-2x2',
    label: '四分屏',
    visiblePaneIds: Object.freeze(['pane-0', 'pane-1', 'pane-2', 'pane-3']),
  }),
});

export function isValidLayoutMode(mode) {
  return typeof mode === 'string' && Object.prototype.hasOwnProperty.call(PRESETS, mode);
}

export function normalizeLayoutMode(mode) {
  return isValidLayoutMode(mode) ? mode : 'single';
}

export function getPreset(mode) {
  return PRESETS[normalizeLayoutMode(mode)];
}

export function visiblePaneIds(mode) {
  return [...getPreset(mode).visiblePaneIds];
}

export function paneLabel(mode) {
  return getPreset(mode).label;
}

export function allPresets() {
  return [PRESETS.single, ...LAYOUT_MODES.map((mode) => PRESETS[mode])];
}
