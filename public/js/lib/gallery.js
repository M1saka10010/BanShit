'use strict';

/* 梗图预览对话框：图片切换、键盘与触摸导航、复制操作；单击图片进入全屏查看。 */

import { $, node } from './dom.js';
import { copyMeme } from './clipboard.js';
import { createGalleryZoom } from './gallery-zoom.js';

const FULLSCREEN_HINT = '单击放大/还原 · 滚轮或双指缩放 · 放大后拖动 · Esc 退出全屏';

export function openGallery(meme, start = 0) {
  const images = Array.isArray(meme.images) ? meme.images : [];
  const opener = document.activeElement;
  const dialog = node('dialog', 'gallery-dialog');
  dialog.setAttribute('aria-label', '梗图预览');

  const toolbar = node('div', 'gallery-toolbar');
  const count = node('span');
  count.setAttribute('aria-live', 'polite');
  const close = node('button', 'secondary', '关闭 ✕');
  toolbar.append(count, close);

  const stage = node('div', 'gallery-stage');
  const openButton = node('button', 'gallery-open');
  const image = node('img', 'gallery-image');
  image.draggable = false;
  openButton.append(image);
  const prev = node('button', 'secondary gallery-prev', '‹');
  const next = node('button', 'secondary gallery-next', '›');
  prev.setAttribute('aria-label', '上一张');
  next.setAttribute('aria-label', '下一张');
  stage.append(openButton, prev, next);
  const openHint = node('p', 'gallery-open-hint', '单击图片进入全屏查看');

  const actions = node('div', 'card-actions preview-actions');
  const message = node('p', 'gallery-message');
  message.setAttribute('role', 'status');
  const syncMessage = () => {
    message.textContent = $('toast').textContent;
  };
  const copyCurrent = node('button', 'secondary', '复制当前图');
  copyCurrent.hidden = images.length === 0;
  copyCurrent.onclick = async () => {
    await copyMeme(meme, 'image', copyCurrent, current);
    syncMessage();
  };
  if (images.length) actions.prepend(copyCurrent);
  for (const [kind, label, available] of [
    ['text', '复制文字', Boolean(meme.text)],
    ['album', '整组拼图复制', images.length > 1],
  ]) {
    if (!available) continue;
    const button = node('button', 'secondary', label);
    button.type = 'button';
    button.onclick = async () => {
      await copyMeme(meme, kind, button);
      syncMessage();
    };
    actions.append(button);
  }

  let current = Number.isInteger(start) ? Math.max(0, Math.min(start, images.length - 1)) : 0;
  let fullscreen = null;

  const update = () => {
    stage.hidden = images.length === 0;
    openHint.hidden = images.length === 0;
    if (images.length) {
      image.src = images[current];
      openButton.setAttribute('aria-label', '全屏查看第 ' + (current + 1) + ' 张图片');
    }
    image.alt = '第 ' + (current + 1) + ' 张图片';
    const label = images.length ? current + 1 + ' / ' + images.length : '文字预览';
    count.textContent = label;
    prev.disabled = current === 0;
    next.disabled = current === images.length - 1;
    prev.hidden = next.hidden = images.length < 2;
    if (fullscreen) {
      if (images.length) fullscreen.image.src = images[current];
      fullscreen.image.alt = image.alt;
      fullscreen.count.textContent = label;
      fullscreen.hint.textContent = FULLSCREEN_HINT;
      fullscreen.prev.disabled = prev.disabled;
      fullscreen.next.disabled = next.disabled;
      fullscreen.prev.hidden = fullscreen.next.hidden = images.length < 2;
      fullscreen.original.removeAttribute('href');
      if (images.length) {
        try {
          const url = new URL(images[current], document.baseURI);
          if (['http:', 'https:'].includes(url.protocol)) fullscreen.original.href = url.href;
        } catch {}
      }
      fullscreen.zoom.setReady(false);
    }
    message.textContent = '';
  };
  const move = (delta) => {
    const target = current + delta;
    if (target >= 0 && target < images.length) {
      current = target;
      update();
    }
  };

  const buildFullscreen = () => {
    const layer = node('dialog', 'gallery-dialog gallery-fullscreen');
    layer.setAttribute('aria-label', '全屏查看');
    const layerToolbar = node('div', 'gallery-toolbar');
    const layerCount = node('span');
    layerCount.setAttribute('aria-live', 'polite');
    const layerClose = node('button', 'secondary', '退出全屏 ✕');
    layerToolbar.append(layerCount, layerClose);
    const layerStage = node('div', 'gallery-stage');
    const viewport = node('div', 'gallery-viewport');
    viewport.tabIndex = 0;
    viewport.setAttribute('role', 'group');
    viewport.setAttribute('aria-label', '全屏图片：单击或回车放大/还原，+/- 缩放，0 还原，放大后方向键平移');
    const layerImage = node('img', 'gallery-image');
    layerImage.draggable = false;
    viewport.append(layerImage);
    const layerPrev = node('button', 'secondary gallery-prev', '‹');
    const layerNext = node('button', 'secondary gallery-next', '›');
    layerPrev.setAttribute('aria-label', '上一张');
    layerNext.setAttribute('aria-label', '下一张');
    layerStage.append(viewport, layerPrev, layerNext);
    const layerActions = node('div', 'gallery-zoom-actions');
    const zoomOut = node('button', 'secondary', '缩小 −');
    const zoomLevel = node('output', 'gallery-zoom-level', '100%');
    zoomLevel.setAttribute('aria-label', '缩放比例');
    const zoomIn = node('button', 'secondary', '放大 +');
    const reset = node('button', 'secondary', '还原');
    const original = node('a', '', '查看原图 ↗');
    original.target = '_blank';
    original.rel = 'noopener noreferrer';
    const hint = node('span', 'gallery-zoom-hint', FULLSCREEN_HINT);
    layerActions.append(zoomOut, zoomLevel, zoomIn, reset, original, hint);
    layer.append(layerToolbar, layerStage, layerActions);

    const zoom = createGalleryZoom(viewport, layerImage, {
      onSwipe: (delta) => move(delta),
      onChange: ({ scale, ready }) => {
        zoomOut.disabled = reset.disabled = !ready || scale === 1;
        zoomIn.disabled = !ready || scale === 5;
        zoomLevel.textContent = Math.round(scale * 100) + '%';
      },
    });
    zoomOut.onclick = () => zoom.zoomBy(1 / 1.25);
    zoomIn.onclick = () => zoom.zoomBy(1.25);
    reset.onclick = () => zoom.reset();
    layerClose.onclick = () => layer.close();
    layerPrev.onclick = () => move(-1);
    layerNext.onclick = () => move(1);
    layerImage.onload = () => zoom.setReady(layerImage.naturalWidth > 0);
    layerImage.onerror = () => {
      zoom.setReady(false);
      hint.textContent = '图片加载失败，可切换其他图片。';
    };
    layer.addEventListener('click', (event) => {
      if (event.target === layer) layer.close();
    });
    layer.addEventListener('keydown', (event) => {
      if (images.length && (event.key === 'ArrowLeft' || event.key === 'ArrowRight')) {
        event.preventDefault();
        move(event.key === 'ArrowLeft' ? -1 : 1);
      }
    });
    layer.addEventListener('close', () => {
      zoom.reset();
      openButton.focus();
    });
    document.body.append(layer);
    return { layer, viewport, image: layerImage, count: layerCount, prev: layerPrev, next: layerNext, original, hint, zoom };
  };

  const enterFullscreen = () => {
    if (!images.length) return;
    if (!fullscreen) fullscreen = buildFullscreen();
    update();
    fullscreen.layer.showModal();
    fullscreen.viewport.focus();
  };

  prev.onclick = () => move(-1);
  next.onclick = () => move(1);
  openButton.onclick = enterFullscreen;
  close.onclick = () => dialog.close();
  image.onerror = () => {
    message.textContent = '图片加载失败，可切换其他图片。';
  };
  const listeners = new AbortController();
  dialog.addEventListener('keydown', (event) => {
    if (images.length && (event.key === 'ArrowLeft' || event.key === 'ArrowRight')) {
      event.preventDefault();
      move(event.key === 'ArrowLeft' ? -1 : 1);
    }
  }, { signal: listeners.signal });
  dialog.addEventListener('click', (event) => {
    if (event.target === dialog) dialog.close();
  }, { signal: listeners.signal });
  dialog.addEventListener('close', () => {
    listeners.abort();
    if (fullscreen) {
      fullscreen.zoom.destroy();
      fullscreen.layer.remove();
      fullscreen = null;
    }
    image.onload = image.onerror = null;
    for (const button of dialog.querySelectorAll('button')) button.onclick = null;
    document.body.classList.remove('gallery-open');
    const toast = $('toast');
    if (toast) document.body.appendChild(toast);
    dialog.remove();
    if (opener?.isConnected) opener.focus();
  }, { once: true });

  dialog.append(toolbar, stage, openHint);
  if (meme.text) dialog.append(node('p', 'meme-text preview-text', meme.text));
  dialog.append(actions, message);
  document.body.append(dialog);
  update();
  dialog.showModal();
  document.body.classList.add('gallery-open');
  close.focus();
}
