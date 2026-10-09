import { build } from 'esbuild';
import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';

const options = {
  entryPoints: ['src/native/claude-quota.js'], bundle: true,
  platform: 'node', format: 'cjs', target: 'node18', minify: true,
  outfile: '../internal/provider/claude_quota_helper.cjs',
  define: { 'import.meta.url': '__helperModuleURL' },
  banner: { js: '// Generated from Claude Agent SDK 0.3.293. See docs/maintenance.md.\nconst __helperModuleURL = require("url").pathToFileURL(require("path").resolve("claude-quota-helper.cjs")).href;' },
};
const result = await build({ ...options, write: false, metafile: true });
const packages = [...new Set(Object.keys(result.metafile.inputs)
  .filter(path => path.includes('node_modules/'))
  .map(path => {
    const relative = path.split('node_modules/').at(-1);
    return relative.split('/').slice(0, relative.startsWith('@') ? 2 : 1).join('/');
  }))].sort();
const notices = packages.map(name => {
  const root = join('node_modules', name);
  const pkg = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'));
  const file = ['LICENSE', 'LICENSE.md', 'LICENSE.txt', 'LICENSE-MIT', 'license'].find(file => existsSync(join(root, file)));
  if (!file) throw new Error(`Missing bundled dependency license: ${name}`);
  return `${name} ${pkg.version}\n${readFileSync(join(root, file), 'utf8').trim()}\n`;
}).join('\n');
writeFileSync('../docs/claude-quota-third-party-notices.txt', notices);
options.banner.js += '\n/*\n' + notices.replaceAll('*/', '* /') + '\n*/';
await build(options);
