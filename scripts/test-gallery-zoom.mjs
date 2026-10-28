import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const source = await readFile(new URL('../public/js/lib/gallery-zoom.js', import.meta.url), 'utf8');
const { createGalleryZoom } = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));

class Target {
  listeners = new Map();
  captures = new Set();
  classList = { toggle() {} };
  clientWidth = 400;
  clientHeight = 300;
  addEventListener(type, fn) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set());
    this.listeners.get(type).add(fn);
  }
  removeEventListener(type, fn) { this.listeners.get(type).delete(fn); }
  setAttribute() {}
  setPointerCapture(id) { this.captures.add(id); }
  hasPointerCapture(id) { return this.captures.has(id); }
  releasePointerCapture(id) { this.captures.delete(id); this.emit('lostpointercapture', { pointerId: id }); }
  getBoundingClientRect() { return { left: 0, top: 0, width: 400, height: 300 }; }
  emit(type, fields = {}) {
    const event = { button: 0, detail: 1, clientX: 200, clientY: 150, pointerId: 1,
      preventDefault() { this.prevented = true; }, stopPropagation() { this.stopped = true; }, ...fields };
    for (const fn of this.listeners.get(type) || []) fn(event);
    return event;
  }
}

function setup(t) {
  globalThis.window = new Target();
  const viewport = new Target();
  const image = { offsetWidth: 400, offsetHeight: 300, style: {} };
  const swipes = [];
  let state;
  const zoom = createGalleryZoom(viewport, image, { onChange: (value) => { state = value; }, onSwipe: (delta) => swipes.push(delta) });
  t.after(() => zoom.destroy());
  return { viewport, image, zoom, swipes, state: () => state };
}

test('loading/error disables zoom; click toggles; bounds and reset', (t) => {
  const { viewport, zoom, state, image } = setup(t);
  viewport.emit('click');
  zoom.zoomBy(2);
  assert.equal(state().scale, 1);
  assert.equal(viewport.emit('wheel', { deltaY: -100 }).prevented, undefined);
  zoom.setReady(true);
  viewport.emit('click');
  assert.equal(state().scale, 2);
  viewport.emit('click');
  assert.equal(state().scale, 1);
  zoom.zoomBy(100);
  assert.equal(state().scale, 5);
  zoom.zoomBy(0.001);
  assert.equal(state().scale, 1);
  zoom.zoomBy(2);
  zoom.setReady(false);
  assert.deepEqual(state(), { scale: 1, ready: false });
  assert.equal(image.style.transform, 'translate(0px, 0px) scale(1)');
});

test('wheel anchors at pointer, clamps translation, keyboard pans and resets', (t) => {
  const { viewport, zoom, state, image } = setup(t);
  zoom.setReady(true);
  const wheel = viewport.emit('wheel', { deltaY: -100, deltaMode: 1, clientX: 300 });
  assert.equal(wheel.prevented, true);
  assert.ok(state().scale > 1);
  assert.ok(image.style.transform.startsWith('translate(-'));
  zoom.reset();
  viewport.emit('keydown', { key: 'Enter' });
  assert.equal(state().scale, 2);
  assert.equal(viewport.emit('keydown', { key: 'ArrowLeft' }).stopped, true);
  assert.equal(image.style.transform, 'translate(40px, 0px) scale(2)');
  viewport.emit('pointerdown');
  viewport.emit('pointermove', { clientX: 2000, clientY: 2000 });
  viewport.emit('pointerup', { clientX: 2000, clientY: 2000 });
  viewport.emit('click');
  assert.equal(image.style.transform, 'translate(200px, 150px) scale(2)');
  viewport.emit('keydown', { key: '0' });
  assert.equal(state().scale, 1);
  assert.equal(viewport.emit('keydown', { key: 'ArrowLeft' }).stopped, undefined);
});

test('single-pointer swipe only at fit, vertical swipe ignored, synthetic click suppressed', (t) => {
  const { viewport, zoom, state, swipes } = setup(t);
  zoom.setReady(true);
  viewport.emit('pointerdown');
  viewport.emit('pointerup', { clientX: 100 });
  viewport.emit('click');
  assert.deepEqual(swipes, [1]);
  assert.equal(state().scale, 1);
  viewport.emit('pointerdown');
  viewport.emit('pointerup', { clientX: 100, clientY: 400 });
  zoom.zoomBy(2);
  viewport.emit('pointerdown');
  viewport.emit('pointermove', { clientX: 300 });
  viewport.emit('pointerup', { clientX: 300 });
  assert.deepEqual(swipes, [1]);
});

test('pinch, remaining-finger drag, and pinch returning to fit never swipe', (t) => {
  const { viewport: v, zoom, state, swipes, image } = setup(t);
  zoom.setReady(true);
  v.emit('pointerdown', { clientX: 100 });
  v.emit('pointerdown', { pointerId: 2, clientX: 200 });
  v.emit('pointermove', { pointerId: 2, clientX: 300 });
  assert.equal(state().scale, 2);
  v.emit('pointerup', { pointerId: 2, clientX: 300 });
  const before = image.style.transform;
  v.emit('pointermove', { clientX: 150 });
  assert.notEqual(image.style.transform, before);
  v.emit('pointerup', { clientX: 350 });
  v.emit('click');
  assert.equal(state().scale, 2);
  zoom.reset();
  v.emit('pointerdown', { clientX: 100 });
  v.emit('pointerdown', { pointerId: 2, clientX: 200 });
  v.emit('pointermove', { pointerId: 2, clientX: 150 });
  v.emit('pointerup', { pointerId: 2, clientX: 150 });
  v.emit('pointerup', { clientX: 350 });
  assert.equal(state().scale, 1);
  assert.deepEqual(swipes, []);
});

test('cancellation, image switch, resize and destroy release capture/listeners', (t) => {
  const { viewport: v, zoom, state, swipes } = setup(t);
  zoom.setReady(true);
  for (const type of ['pointercancel', 'lostpointercapture']) {
    v.emit('pointerdown');
    v.emit(type);
    v.emit('pointerup', { clientX: 400 });
    v.emit('click');
    assert.equal(state().scale, 1);
  }
  zoom.zoomBy(2);
  window.emit('resize');
  assert.equal(state().scale, 1);
  v.emit('pointerdown');
  zoom.setReady(false);
  v.emit('pointerup', { clientX: 400 });
  assert.equal(v.captures.size, 0);
  assert.deepEqual(swipes, []);
  zoom.destroy();
  assert.ok([...v.listeners.values(), ...window.listeners.values()].every((set) => set.size === 0));
});
