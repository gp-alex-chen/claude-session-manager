const MIME = 'application/x-theone-session-reference';
let dragging = null;

export function beginReferenceDrag(session, event) {
  dragging = null;
  if (session.provider !== 'codex' || session.temporary
    || !/^codex:[0-9a-f-]{36}$/i.test(session.id) || !event.dataTransfer) {
    event.preventDefault?.();
    return;
  }
  dragging = { id: session.id };
  event.stopPropagation?.();
  event.dataTransfer.effectAllowed = 'copy';
  event.dataTransfer.setData(MIME, session.id);
}

export function peekReferenceDrag() { return dragging; }
export function endReferenceDrag() { dragging = null; }
export function takeReferenceDrag(event) {
  const selected = dragging;
  dragging = null;
  return selected && event.dataTransfer?.getData(MIME) === selected.id ? selected : null;
}

export function referenceText(ref) {
  if (!/^ref_[0-9a-f]{32}$/.test(ref?.reference)) throw new Error('无效会话引用');
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(ref.sessionId)) throw new Error('无效会话 ID');
  const title = Array.from(String(ref.name || 'Codex').replace(/[\x00-\x1f\x7f-\x9f\u2028\u2029]/g, ' '));
  const name = title.slice(0, 32).join('') + (title.length > 32 ? '…' : '');
  const label = name.replace(/[\\[\]]/g, '\\$&');
  return `[@${label}](thread://${ref.sessionId}) `;
}
