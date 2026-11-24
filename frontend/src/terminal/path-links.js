const TOKENS = /(["'`])([^\r\n]+?)\1|[^\s"'`<>(){}\[\]|，。！？；：（）【】《》、“”‘’]+/gu;
const MAX_WRAPPED_ROWS = 32;

function pathTarget(label) {
  if (/^file:\/\//i.test(label)) return label;
  const target = label.replace(/:\d+(?::\d+)?$/, '');
  const drivePath = /^[a-z]:[\\/]/i.test(target);
  const name = drivePath ? target.slice(3) : target;
  if (/[:<>|?*\x00-\x1f]/.test(name) || (!drivePath && /^[\\/]/.test(name))) return '';
  if (drivePath || /[\\/]/.test(name) || /^[^.\s].*\.[a-z][a-z0-9_-]*$/i.test(name)) return target;
  return '';
}

function readWrappedLine(buffer, y) {
  const row = y - 1;
  if (!buffer.getLine(row)) return null;
  let first = row;
  while (first > 0 && buffer.getLine(first).isWrapped) {
    first -= 1;
    if (row - first >= MAX_WRAPPED_ROWS) return null;
  }
  let last = row;
  while (buffer.getLine(last + 1)?.isWrapped) {
    last += 1;
    if (last - first >= MAX_WRAPPED_ROWS) return null;
  }

  let text = '';
  const positions = [];
  const cell = buffer.getNullCell();
  for (let rowIndex = first; rowIndex <= last; rowIndex++) {
    const line = buffer.getLine(rowIndex);
    const nextStartsWide = rowIndex < last && buffer.getLine(rowIndex + 1).getCell(0)?.getWidth() === 2;
    for (let col = 0; col < line.length; col++) {
      const current = line.getCell(col, cell);
      const width = current.getWidth();
      if (!width) continue;
      const chars = current.getChars();
      // xterm leaves an empty final cell when a wide character wraps.
      if (!chars && col === line.length - 1 && nextStartsWide) continue;
      const value = chars || ' ';
      text += value;
      const position = { x: col + 1, y: rowIndex + 1, width };
      for (let i = 0; i < value.length; i++) positions.push(position);
    }
  }
  return { text, positions };
}

export function createPathLinkProvider(term, activate) {
  return {
    provideLinks(y, callback) {
      const line = readWrappedLine(term.buffer.active, y);
      if (!line) {
        callback(undefined);
        return;
      }
      const links = [];
      for (const match of line.text.matchAll(TOKENS)) {
        const quoted = match[2] !== undefined;
        // Preserve path components such as /. and /.. when trimming prose.
        const label = quoted ? match[2] : match[0].replace(/[,;!]+$/, '').replace(/([^./\\])\.$/, '$1');
        const target = pathTarget(label);
        if (!target) continue;
        const index = match.index + (quoted ? 1 : 0);
        const first = line.positions[index];
        const last = line.positions[index + label.length - 1];
        if (y < first.y || y > last.y) continue;
        links.push({
          text: target,
          range: { start: { x: first.x, y: first.y }, end: { x: last.x + last.width - 1, y: last.y } },
          activate,
        });
      }
      callback(links.length ? links : undefined);
    },
  };
}

export function createTerminalLinkHandler(activatePath, view) {
  return {
    allowNonHttpProtocols: true,
    activate(event, uri) {
      if (event.button !== 0) return;
      if (/^file:/i.test(uri)) {
        activatePath(event, uri);
        return;
      }
      let url;
      try { url = new URL(uri); } catch { return; }
      if (url.protocol !== 'http:' && url.protocol !== 'https:') return;
      if (view?.confirm(`Do you want to navigate to ${uri}?\n\nWARNING: This link could potentially be dangerous`)) {
        view.open(url.href, '_blank', 'noopener,noreferrer');
      }
    },
  };
}
