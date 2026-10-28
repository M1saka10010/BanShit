import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const moduleURL = (source) => 'data:text/javascript;base64,' + Buffer.from(source).toString('base64');
const dom = moduleURL(await readFile(new URL('../public/js/lib/dom.js', import.meta.url), 'utf8'));
const source = await readFile(new URL('../public/js/lib/clipboard.js', import.meta.url), 'utf8');
const { copyMeme, pngBlob, albumBlob } = await import(moduleURL(source.replace("'./dom.js'", JSON.stringify(dom))));
const images = ['a', 'b'].map((x) => '/media/' + x.repeat(64) + '.png');
const meme = { id: 'test/id', text: 'hello', images };
let state;
const parent = () => ({ appendChild(child) { child.parentNode = this; } });
const body = parent();
const dialog = parent();
const toast = { parentNode: body };
globalThis.setTimeout = () => 1;
globalThis.clearTimeout = () => {};
globalThis.document = {
  body,
  getElementById: () => toast,
  querySelector: () => state.modal ? dialog : null,
  createElement: () => {
    const canvas = {
      getContext: () => ({ drawImage() {}, fillRect() {} }),
      toBlob(callback, type) {
        const blob = new Blob(['png'], { type });
        blob.width = canvas.width;
        blob.height = canvas.height;
        state.canvases.push([canvas.width, canvas.height]);
        callback(blob);
      },
    };
    return canvas;
  },
};
globalThis.Image = class {
  async decode() {
    if (state.decodeError) throw new Error('decode failed');
    [this.naturalWidth, this.naturalHeight] = state.sizes.shift() || [10, 20];
  }
};
globalThis.createImageBitmap = async (blob) => ({
  width: blob.width, height: blob.height, close() { state.closed++; },
});
URL.createObjectURL = () => { state.created++; return 'blob:test'; };
URL.revokeObjectURL = () => { state.revoked++; };
globalThis.fetch = async (url, options) => {
  if (options.method === 'POST') {
    assert.equal(state.written, true, 'report only after clipboard success');
    state.reports.push({ url, body: JSON.parse(options.body) });
    return { ok: !state.reportError };
  }
  state.loads.push(url);
  assert.deepEqual(options, { credentials: 'same-origin', redirect: 'error' });
  return { ok: !state.fetchError, blob: async () => new Blob(['source']) };
};
const clipboard = {
  async write(items) {
    state.writes++;
    if (state.writeError) throw new DOMException('permission denied', 'NotAllowedError');
    assert.equal((await items[0].data['image/png']).type, 'image/png');
    state.written = true;
  },
  async writeText(text) {
    assert.equal(text, meme.text);
    if (state.writeError) throw new DOMException('permission denied', 'NotAllowedError');
    state.written = true;
  },
};
Object.defineProperty(globalThis, 'navigator', { value: { clipboard }, configurable: true });
globalThis.ClipboardItem = class {
  constructor(data) {
    state.constructors++;
    if (state.constructorError) throw new Error('constructor failed');
    if (state.blobOnly && !(data['image/png'] instanceof Blob)) throw new TypeError('Blob required');
    this.data = data;
  }
};
globalThis.window = { isSecureContext: true, ClipboardItem };
function reset(options = {}) {
  state = { reports: [], loads: [], sizes: [], canvases: [], created: 0, revoked: 0,
    closed: 0, writes: 0, constructors: 0, written: false, ...options };
  window.isSecureContext = true;
  window.ClipboardItem = ClipboardItem;
  navigator.clipboard = clipboard;
}
async function copy(kind, options = {}) {
  reset(options);
  const button = { disabled: false };
  const pending = copyMeme(meme, kind, button, 1);
  if (!options.constructorError) assert.equal(button.disabled, true);
  if (kind !== 'text' && !options.blobOnly && !options.constructorError) {
    assert.equal(state.writes, 1, 'Promise-capable write must start before yielding');
  }
  await pending;
  assert.equal(button.disabled, false);
}
for (const kind of ['image', 'album', 'text']) {
  await copy(kind, { modal: true });
  assert.equal(state.reports.length, 1);
  assert.equal(state.reports[0].url, '/api/memes/test%2Fid/copy');
  assert.deepEqual(state.reports[0].body, kind === 'image' ? { kind, imageIndex: 1 } : { kind });
  assert.equal(toast.parentNode, dialog);
  assert.match(toast.textContent, /已搬走/);
  assert.equal(state.created, state.revoked);
  if (kind === 'image') assert.deepEqual(state.loads, [images[1]]);
  if (kind === 'album') {
    assert.deepEqual(state.canvases.at(-1), [10, 40]);
    assert.equal(state.closed, 2);
  }
  await copy(kind, { writeError: true });
  assert.equal(state.reports.length, 0);
  assert.match(toast.textContent, /复制失败，未记录次数/);
  assert.equal(toast.parentNode, body);
  await new Promise((resolve) => setImmediate(resolve));
  await copy(kind, { reportError: true });
  assert.match(toast.textContent, /已搬走，但搬运统计没能记上/);
}
for (const kind of ['image', 'album']) {
  await copy(kind, { blobOnly: true });
  assert.equal(state.constructors, 2);
  assert.equal(state.reports.length, 1);
  for (const options of [{ fetchError: true }, { decodeError: true }, { sizes: [[8001, 1]] },
    { blobOnly: true, decodeError: true }, { constructorError: true }]) {
    await copy(kind, options);
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(state.reports.length, 0);
    assert.equal(state.created, state.revoked);
  }
}
for (const setup of [() => { window.isSecureContext = false; },
  () => { navigator.clipboard = undefined; }, () => { window.ClipboardItem = undefined; }]) {
  reset();
  setup();
  const button = {};
  await copyMeme(meme, 'image', button);
  assert.equal(button.disabled, false);
  assert.equal(state.reports.length, 0);
  assert.equal(state.loads.length, 0);
  assert.match(toast.textContent, /HTTPS|不支持图片复制/);
}
for (const size of [[8000, 3000], [1500, 16000]]) {
  reset({ sizes: [size] });
  assert.equal((await pngBlob(images[0])).type, 'image/png');
}
for (const size of [[8001, 1], [1, 16001], [8000, 3001]]) {
  reset({ sizes: [size] });
  await assert.rejects(pngBlob(images[0]), /尺寸过大/);
  assert.equal(state.created, state.revoked);
}
for (const sizes of [[[1, 8001], [1, 8000]], [[8000, 2000], [8000, 2000]], [[8000, 1], [1, 4000]]]) {
  reset({ sizes });
  await assert.rejects(albumBlob(images), /过大/);
  assert.equal(state.closed, 2);
}
reset();
await assert.rejects(pngBlob('/invalid.png'), /格式不可用/);
assert.equal(state.loads.length, 0);
console.log('PASS clipboard: Promise/Blob, activation timing, image/album/text accounting, failures, limits, cleanup, modal notification');
