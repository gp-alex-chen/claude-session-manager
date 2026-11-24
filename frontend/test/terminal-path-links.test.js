import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { createPathLinkProvider } from '../src/terminal/path-links.js';

globalThis.self ??= globalThis;
const { Terminal } = createRequire(import.meta.url)('@xterm/xterm');

async function writeTerminal(t, output, cols) {
  const term = new Terminal({ cols, rows: 8 });
  t.after(() => term.dispose());
  await new Promise((resolve) => term.write(output, resolve));
  return term;
}

function linksOn(provider, row) {
  let links;
  provider.provideLinks(row, (value) => { links = value; });
  return links;
}

test('terminal paths are extracted from displayed text and retain their cell ranges', async (t) => {
  const term = await writeTerminal(t,
    '\x1b[4:4moutputs/windows/portable\x1b[0m，(src/main.go:12:3) D:\\out\\build.exe build.log ../..\r\n'
    + 'https://example.com/main.go \\\\server\\share\\out.exe C:relative.exe v1.5.1 普通文本', 100);
  const calls = [];
  const provider = createPathLinkProvider(term, (...args) => calls.push(args));
  const links = linksOn(provider, 1);
  assert.deepEqual(links.map(({ text }) => text),
    ['outputs/windows/portable', 'src/main.go', 'D:\\out\\build.exe', 'build.log', '../..']);
  assert.deepEqual(links[0].range, { start: { x: 1, y: 1 }, end: { x: 24, y: 1 } });
  assert.equal(linksOn(provider, 2), undefined);
  assert.equal(calls.length, 0);
  const click = { button: 0, ctrlKey: false };
  links[0].activate(click, links[0].text);
  assert.deepEqual(calls, [[click, 'outputs/windows/portable']]);
});

test('both rows of a soft-wrapped path link to the full path with spaces and wide characters', async (t) => {
  const term = await writeTerminal(t, '产物 "./输出目录/构建 文件.exe"', 20);
  assert.equal(term.buffer.active.getLine(1).isWrapped, true);
  const provider = createPathLinkProvider(term, () => {});
  for (const row of [1, 2]) {
    const [link] = linksOn(provider, row);
    assert.equal(link.text, './输出目录/构建 文件.exe');
    assert.deepEqual(link.range, { start: { x: 7, y: 1 }, end: { x: 11, y: 2 } });
  }
});
