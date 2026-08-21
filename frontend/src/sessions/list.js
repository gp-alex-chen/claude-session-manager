export function leafOf(dir) {
  const parts = String(dir || '').replaceAll('\\', '/').split('/');
  return parts[parts.length - 1] || dir;
}
