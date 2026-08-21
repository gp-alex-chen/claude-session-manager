export const DEFAULT_TERMINAL_FONT_SIZE = 14;
export const MIN_TERMINAL_FONT_SIZE = 10;
export const MAX_TERMINAL_FONT_SIZE = 24;
export const TERMINAL_FONT_SIZE_STEP = 1;

export function normalizeTerminalFontSize(value) {
  if (value === null || value === undefined) return DEFAULT_TERMINAL_FONT_SIZE;
  if (typeof value === 'string' && value.trim() === '') return DEFAULT_TERMINAL_FONT_SIZE;

  const numeric = Number(value);
  if (!Number.isFinite(numeric)) return DEFAULT_TERMINAL_FONT_SIZE;

  return Math.min(
    MAX_TERMINAL_FONT_SIZE,
    Math.max(MIN_TERMINAL_FONT_SIZE, Math.round(numeric)),
  );
}
