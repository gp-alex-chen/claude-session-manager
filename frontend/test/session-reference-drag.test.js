import test from 'node:test';
import assert from 'node:assert/strict';
import { beginReferenceDrag, endReferenceDrag, peekReferenceDrag, takeReferenceDrag, referenceText } from '../src/sessions/reference-drag.js';

const id = 'codex:01900000-0000-7000-8000-000000000001';
function transfer() {
  const data = new Map();
  return { setData: (key, value) => data.set(key, value), getData: key => data.get(key) || '' };
}

test('only an internal persisted Codex drag creates a consumable reference', () => {
  const dataTransfer = transfer();
  endReferenceDrag();
  assert.equal(takeReferenceDrag({ dataTransfer }), null);
  let prevented = false;
  beginReferenceDrag({ id, provider: 'claude' }, { dataTransfer, preventDefault: () => { prevented = true; } });
  assert.equal(prevented, true);
  beginReferenceDrag({ id, provider: 'codex' }, { dataTransfer });
  assert.equal(dataTransfer.effectAllowed, 'copy');
  assert.equal(peekReferenceDrag().id, id);
  assert.equal(takeReferenceDrag({ dataTransfer }).id, id);
  assert.equal(peekReferenceDrag(), null);
});

test('reference text cannot inject terminal control sequences or submit input', () => {
  const text = referenceText({ reference: 'ref_' + 'a'.repeat(32), sessionId: id.slice(6), name: 'name\r\n\x1b[31m\tbad' });
  assert.doesNotMatch(text, /[\x00-\x1f\x7f-\x9f\u2028\u2029]/);
  assert.match(text, /^\[@.*\]\(thread:\/\/[0-9a-f-]{36}\) $/);
  assert.doesNotMatch(text, /ref_|read_session|send_message|theone_sessions/);
});

test('long conversation titles remain compact without losing the target UUID', () => {
  const text = referenceText({ reference: 'ref_' + 'a'.repeat(32), sessionId: id.slice(6), name: '会'.repeat(120) });
  assert.ok(new TextEncoder().encode(text).length < 200);
  assert.match(text, /…\]\(thread:\/\/01900000-0000-7000-8000-000000000001\)/);
});
