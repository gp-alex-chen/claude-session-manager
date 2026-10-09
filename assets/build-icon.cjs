// Design-time export only. Requires Playwright and an installed Microsoft Edge.
// Normal frontend/Go builds consume the checked-in ICO and .syso resources.
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');

async function main() {
  const root = path.resolve(__dirname, '..');
  const out = path.join(root, '.codex-build', 'theone-icons');
  fs.mkdirSync(out, { recursive: true });
  const svg = fs.readFileSync(path.join(__dirname, 'icon.svg'), 'utf8');
  const browser = await chromium.launch({ channel: 'msedge', headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 512, height: 512 }, deviceScaleFactor: 1 });
    const frames = [];
    for (const size of [16, 20, 24, 32, 40, 48, 64, 128, 256]) {
      await page.setContent(`<style>html,body{margin:0;background:transparent}svg{display:block;width:${size}px;height:${size}px}</style>${svg}`);
      const png = await page.screenshot({ omitBackground: true, clip: { x: 0, y: 0, width: size, height: size } });
      fs.writeFileSync(path.join(out, `icon-${size}.png`), png);
      frames.push({ size, png });
    }
    const directory = Buffer.alloc(6 + 16 * frames.length);
    directory.writeUInt16LE(1, 2);
    directory.writeUInt16LE(frames.length, 4);
    let offset = directory.length;
    frames.forEach(({ size, png }, index) => {
      const entry = 6 + 16 * index;
      directory[entry] = directory[entry + 1] = size === 256 ? 0 : size;
      directory.writeUInt16LE(1, entry + 4);
      directory.writeUInt16LE(32, entry + 6);
      directory.writeUInt32LE(png.length, entry + 8);
      directory.writeUInt32LE(offset, entry + 12);
      offset += png.length;
    });
    fs.writeFileSync(path.join(__dirname, 'icon.ico'), Buffer.concat([directory, ...frames.map(frame => frame.png)]));
    fs.writeFileSync(path.join(__dirname, 'icon.png'), frames.at(-1).png);
    console.log(`Exported ${frames.length} ICO sizes, 16–256 px, and icon.png`);
  } finally {
    await browser.close();
  }
}

main().catch(error => { console.error(error); process.exitCode = 1; });
