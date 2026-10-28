'use strict';

export function createGalleryZoom(viewport, image, { onChange, onSwipe }) {
  let scale = 1;
  let x = 0;
  let y = 0;
  let ready = false;
  let gesture = null;
  let suppressClick = false;
  const pointers = new Map();
  const cleanup = [];
  const listen = (target, type, handler, options) => {
    target.addEventListener(type, handler, options);
    cleanup.push(() => target.removeEventListener(type, handler, options));
  };
  const render = () => {
    const maxX = Math.max(0, (image.offsetWidth * scale - viewport.clientWidth) / 2);
    const maxY = Math.max(0, (image.offsetHeight * scale - viewport.clientHeight) / 2);
    x = scale === 1 ? 0 : Math.max(-maxX, Math.min(maxX, x));
    y = scale === 1 ? 0 : Math.max(-maxY, Math.min(maxY, y));
    image.style.transform = `translate(${x}px, ${y}px) scale(${scale})`;
    viewport.classList.toggle('is-zoomed', scale > 1);
    viewport.classList.toggle('is-ready', ready);
    viewport.setAttribute('aria-disabled', String(!ready));
    onChange({ scale, ready });
  };
  const zoomTo = (value, point) => {
    if (!ready) return;
    const next = Math.max(1, Math.min(5, value));
    if (point) {
      const rect = viewport.getBoundingClientRect();
      const px = point.x - rect.left - rect.width / 2;
      const py = point.y - rect.top - rect.height / 2;
      x = px - (px - x) * next / scale;
      y = py - (py - y) * next / scale;
    }
    scale = next;
    render();
  };
  const clearPointers = () => {
    const ids = [...pointers.keys()];
    pointers.clear();
    gesture = null;
    for (const id of ids) {
      if (viewport.hasPointerCapture(id)) viewport.releasePointerCapture(id);
    }
  };
  const reset = () => {
    clearPointers();
    scale = 1;
    x = y = 0;
    render();
  };
  const pair = () => {
    const [a, b] = [...pointers.values()];
    return { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2, distance: Math.hypot(a.x - b.x, a.y - b.y) };
  };
  listen(viewport, 'pointerdown', (event) => {
    if (event.button !== 0) return;
    const point = { x: event.clientX, y: event.clientY };
    if (!pointers.size) {
      suppressClick = false;
      gesture = { start: point, multi: false, moved: false, swipe: scale === 1 };
    }
    pointers.set(event.pointerId, point);
    viewport.setPointerCapture(event.pointerId);
    if (pointers.size > 1) {
      gesture.multi = true;
      suppressClick = true;
      gesture.pair = pair();
    }
  });
  listen(viewport, 'pointermove', (event) => {
    if (!pointers.has(event.pointerId)) return;
    const point = { x: event.clientX, y: event.clientY };
    const previous = pointers.get(event.pointerId);
    pointers.set(event.pointerId, point);
    if (Math.hypot(point.x - gesture.start.x, point.y - gesture.start.y) > 6) {
      gesture.moved = true;
      suppressClick = true;
    }
    if (pointers.size > 1) {
      const next = pair();
      const old = gesture.pair;
      if (old.distance > 0) zoomTo(scale * next.distance / old.distance, old);
      if (ready && scale > 1) { x += next.x - old.x; y += next.y - old.y; render(); }
      gesture.pair = next;
    } else if (ready && scale > 1) {
      x += point.x - previous.x;
      y += point.y - previous.y;
      render();
    }
  });
  listen(viewport, 'pointerup', (event) => {
    if (!pointers.has(event.pointerId)) return;
    const dx = event.clientX - gesture.start.x;
    const dy = event.clientY - gesture.start.y;
    const swipe = !gesture.multi && gesture.swipe && scale === 1 &&
      Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy) * 1.5;
    suppressClick ||= gesture.multi || gesture.moved || Math.hypot(dx, dy) > 6;
    pointers.delete(event.pointerId);
    if (viewport.hasPointerCapture(event.pointerId)) viewport.releasePointerCapture(event.pointerId);
    if (pointers.size > 1) gesture.pair = pair();
    if (!pointers.size) {
      gesture = null;
      if (swipe) onSwipe(dx < 0 ? 1 : -1);
    }
  });
  const cancel = (event) => {
    if (!pointers.has(event.pointerId)) return;
    suppressClick = true;
    clearPointers();
  };
  listen(viewport, 'pointercancel', cancel);
  listen(viewport, 'lostpointercapture', cancel);
  listen(viewport, 'click', (event) => {
    if (!suppressClick || event.detail === 0) zoomTo(scale > 1 ? 1 : 2);
  });
  listen(viewport, 'wheel', (event) => {
    if (!ready) return;
    event.preventDefault();
    const delta = event.deltaY * (event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? viewport.clientHeight : 1);
    zoomTo(scale * Math.exp(-Math.max(-500, Math.min(500, delta)) * 0.002), { x: event.clientX, y: event.clientY });
  }, { passive: false });
  listen(viewport, 'keydown', (event) => {
    if (!ready) return;
    if (['Enter', ' '].includes(event.key)) zoomTo(scale > 1 ? 1 : 2);
    else if (['+', '='].includes(event.key)) zoomTo(scale * 1.25);
    else if (event.key === '-') zoomTo(scale / 1.25);
    else if (event.key === '0') reset();
    else if (scale > 1 && ['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key)) {
      x += event.key === 'ArrowLeft' ? 40 : event.key === 'ArrowRight' ? -40 : 0;
      y += event.key === 'ArrowUp' ? 40 : event.key === 'ArrowDown' ? -40 : 0;
      render();
    } else return;
    event.preventDefault();
    event.stopPropagation();
  });
  listen(window, 'resize', reset);
  render();
  return {
    reset,
    zoomBy: (factor) => zoomTo(scale * factor),
    setReady(value) { ready = value; reset(); },
    destroy() { cleanup.forEach((remove) => remove()); clearPointers(); },
  };
}
