export function listSig(list) {
  return list.map((session) => [session.id, session.dir, session.name, session.time, session.provider || 'claude'].join('|')).join('\n');
}

export function pairPendingSessions({ pending, lastLoaded, list, realToNew, newToReal, onPair }) {
  const previousIds = new Set(lastLoaded.map((session) => session.id));
  const newByDirectory = new Map();
  for (const session of list) {
    if ((session.provider || 'claude') !== 'claude') continue;
    if (previousIds.has(session.id) || realToNew.has(session.id)) continue;
    if (!newByDirectory.has(session.dir)) newByDirectory.set(session.dir, []);
    newByDirectory.get(session.dir).push(session.id);
  }

  const remaining = [];
  for (const pendingItem of pending) {
    if ((pendingItem.provider || 'claude') !== 'claude') {
      remaining.push(pendingItem);
      continue;
    }
    const candidates = newByDirectory.get(pendingItem.dir);
    const realId = candidates?.length ? candidates.shift() : null;
    if (!realId) {
      remaining.push(pendingItem);
      continue;
    }
    newToReal.set(pendingItem.token, realId);
    realToNew.set(realId, pendingItem.token);
    onPair?.(pendingItem, realId, list.find((session) => session.id === realId));
  }
  return remaining;
}
