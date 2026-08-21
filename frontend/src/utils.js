export function b64ToBytes(b64) {
  const binary = atob(b64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

export function bytesToB64(bytes) {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary);
}

export function leafOf(dir) {
  const value = String(dir || '');
  const parts = value.replaceAll('\\', '/').split('/');
  return parts[parts.length - 1] || dir;
}

export function clampProgress(value) {
  return Math.max(0, Math.min(100, Number(value) || 0));
}
