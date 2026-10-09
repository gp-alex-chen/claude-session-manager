import { build } from 'esbuild';
import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';

const options = {
  entryPoints: ['src/native/claude-rename.js'],
  bundle: true, platform: 'node', format: 'cjs', target: 'node18', minify: true,
  outfile: '../internal/provider/claude_rename_helper.cjs',
  define: { 'import.meta.url': '__helperModuleURL' },
  // The SDK initializes createRequire even for its offline mutation API.
  // stdin has no module URL, so provide an absolute synthetic module location.
  banner: { js: '// Generated from the official Claude Agent SDK 0.3.293. See docs/native-rename.md.\nconst __helperModuleURL = require("url").pathToFileURL(require("path").resolve("claude-rename-helper.cjs")).href;' },
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
  const licenseFile = ['LICENSE', 'LICENSE.md', 'LICENSE.txt', 'LICENSE-MIT', 'license'].find(file => existsSync(join(root, file)));
  if (!licenseFile) throw new Error(`Missing bundled dependency license: ${name}`);
  return `${name} ${pkg.version}\n${readFileSync(join(root, licenseFile), 'utf8').trim()}\n`;
}).join('\n');
writeFileSync('../docs/claude-rename-third-party-notices.txt', notices);
// Include the original notices in the embedded payload as well as the source distribution.
options.banner.js += '\n/*\n' + notices.replaceAll('*/', '* /') + '\n*/';
await build(options);
