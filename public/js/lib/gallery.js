'use strict';

/* 梗图预览对话框：图片切换、键盘与触摸导航、复制操作。 */

import { $, node } from './dom.js';
import { copyMeme } from './clipboard.js';

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
  const image = node('img', 'gallery-image');
  image.draggable = false;
  const prev = node('button', 'secondary gallery-prev', '‹');
  const next = node('button', 'secondary gallery-next', '›');
  prev.setAttribute('aria-label', '上一张');
  next.setAttribute('aria-label', '下一张');
  stage.append(prev, image, next);

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

  let current = start;
  const update = () => {
    stage.hidden = images.length === 0;
    if (images.length) image.src = images[current];
    image.alt = '第 ' + (current + 1) + ' 张图片';
    count.textContent = images.length ? current + 1 + ' / ' + images.length : '文字预览';
    prev.disabled = current === 0;
    next.disabled = current === images.length - 1;
    prev.hidden = next.hidden = images.length < 2;
    message.textContent = '';
  };
  const move = (delta) => {
    const target = current + delta;
    if (target >= 0 && target < images.length) {
      current = target;
      update();
    }
  };
  prev.onclick = () => move(-1);
  next.onclick = () => move(1);
  close.onclick = () => dialog.close();
  image.onerror = () => {
    message.textContent = '图片加载失败，可切换其他图片。';
  };
  dialog.addEventListener('keydown', (event) => {
    if (images.length && (event.key === 'ArrowLeft' || event.key === 'ArrowRight')) {
      event.preventDefault();
      move(event.key === 'ArrowLeft' ? -1 : 1);
    }
  });

  let touch = null;
  stage.addEventListener('touchstart', (event) => {
    touch = event.touches.length === 1 ? { x: event.touches[0].clientX, y: event.touches[0].clientY } : null;
  }, { passive: true });
  stage.addEventListener('touchend', (event) => {
    if (!touch || !event.changedTouches.length) return;
    const dx = event.changedTouches[0].clientX - touch.x;
    const dy = event.changedTouches[0].clientY - touch.y;
    if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy) * 1.5) move(dx < 0 ? 1 : -1);
    touch = null;
  }, { passive: true });
  stage.addEventListener('touchcancel', () => {
    touch = null;
  });
  dialog.addEventListener('click', (event) => {
    if (event.target === dialog) dialog.close();
  });
  dialog.addEventListener('close', () => {
    document.body.classList.remove('gallery-open');
    const toast = $('toast');
    if (toast) document.body.appendChild(toast);
    dialog.remove();
    if (opener?.isConnected) opener.focus();
  }, { once: true });

  dialog.append(toolbar, stage);
  if (meme.text) dialog.append(node('p', 'meme-text preview-text', meme.text));
  dialog.append(actions, message);
  document.body.append(dialog);
  update();
  dialog.showModal();
  document.body.classList.add('gallery-open');
  close.focus();
}
