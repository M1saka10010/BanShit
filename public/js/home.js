'use strict';

/* 首页：加载推荐信息流，渲染卡片与九宫格预览。 */

import { $, node, dateLabel, browserTimeZoneLabel, sourceURL } from './lib/dom.js';
import { initThemeToggle } from './lib/theme.js';
import { openGallery } from './lib/gallery.js';

/** 渲染单条梗图卡片。index 用于入场动画的交错延迟。 */
function card(meme, index = 0) {
  const article = node('article', 'meme-card');
  article.style.animationDelay = Math.min(index * 45, 360) + 'ms';
  article.tabIndex = 0;
  article.setAttribute('aria-label', '查看 meme 详情');
  article.setAttribute('aria-haspopup', 'dialog');
  article.addEventListener('click', (event) => {
    if (event.target.closest('button, a')) return;
    article.focus({ preventScroll: true });
    openGallery(meme);
  });
  article.addEventListener('keydown', (event) => {
    if (event.target === article && (event.key === 'Enter' || event.key === ' ')) {
      event.preventDefault();
      openGallery(meme);
    }
  });

  const images = Array.isArray(meme.images) ? meme.images : [];
  if (images.length > 0) {
    const grid = node('div', images.length === 1
      ? 'single-preview'
      : 'album-grid' + (images.length === 2 || images.length === 4 ? ' album-grid-two' : ''));
    (meme.thumbnails || images).slice(0, 9).forEach((src, index) => {
      const button = node('button', 'preview-tile');
      button.type = 'button';
      button.setAttribute('aria-label', '查看第 ' + (index + 1) + ' 张，共 ' + images.length + ' 张');
      const image = node('img', 'meme-image');
      image.src = src;
      image.alt = '第 ' + (index + 1) + ' 张图片';
      image.loading = 'lazy';
      button.append(image);
      if (index === 8 && images.length > 9) button.append(node('span', 'preview-more', '+' + (images.length - 9)));
      button.onclick = () => openGallery(meme, index);
      grid.append(button);
    });
    article.append(grid);
  }

  const body = node('div', 'card-body');
  const meta = node('div', 'card-meta');
  meta.append(node('span', 'channel', meme.channel || '快乐来源'), node('span', 'tag-seal', '精选'));
  body.append(meta);
  if (meme.text) body.append(node('p', 'meme-text', meme.text));
  body.append(node('div', 'source-link', dateLabel(meme.createdAt)));
  const href = sourceURL(meme.url);
  if (href) {
    const link = node('a', 'source-link', '查看原帖 ↗');
    link.href = href;
    link.target = '_blank';
    link.rel = 'noopener noreferrer';
    body.append(link);
  }
  article.append(body);
  return article;
}

/** 加载中的占位骨架卡片。 */
function skeletonCard() {
  const article = node('article', 'skeleton-card');
  article.setAttribute('aria-hidden', 'true');
  article.append(node('div', 'skeleton-block skeleton-media'));
  const body = node('div', 'skeleton-body');
  body.append(
    node('div', 'skeleton-block skeleton-line short'),
    node('div', 'skeleton-block skeleton-line'),
    node('div', 'skeleton-block skeleton-line'),
  );
  article.append(body);
  return article;
}

async function loadFeed() {
  $('reload').disabled = true;
  $('feed').setAttribute('aria-busy', 'true');
  $('feed-state').hidden = true;
  $('feed').replaceChildren(...Array.from({ length: 6 }, skeletonCard));
  try {
    const response = await fetch('/api/memes', { credentials: 'same-origin', cache: 'no-store' });
    if (!response.ok) throw new Error('加载失败（' + response.status + '）');
    const data = await response.json();
    if (!Array.isArray(data.memes)) throw new Error('服务器返回了无效内容');
    $('feed').replaceChildren(...data.memes.map((meme, index) => card(meme, index)));
    $('feed-state').hidden = data.memes.length > 0;
    $('feed-state').textContent = '搬屎工还没到货 💩 先去喝杯水，稍后刷新看看。';
    $('feed-meta').textContent = '本期 ' + data.memes.length + ' 坨 · 更新于 ' + dateLabel(data.updatedAt);
  } catch (error) {
    $('feed').replaceChildren();
    $('feed-state').hidden = false;
    $('feed-state').textContent = '搬屎车翻沟里了（' + error.message + '）。请点击「刷新内容」重试。';
  } finally {
    $('reload').disabled = false;
    $('feed').setAttribute('aria-busy', 'false');
  }
}

/** 刊头便便海：按网格 + 随机抖动铺满整个刊头区域，每次访问布局都不同。 */
function poopField() {
  const hero = document.querySelector('.hero');
  if (!hero) return;
  const rect = hero.getBoundingClientRect();
  const cols = Math.max(4, Math.round(rect.width / 140));
  const rows = Math.max(3, Math.round(rect.height / 120));
  const field = node('div', 'poop-field');
  for (let row = 0; row < rows; row++) {
    for (let col = 0; col < cols; col++) {
      const left = (col + 0.15 + Math.random() * 0.7) / cols;
      const top = (row + 0.15 + Math.random() * 0.7) / rows;
      if (left > 0.7 && top < 0.42) continue; // 避开右上角印章
      const poop = document.createElement('i');
      poop.className = 'poop';
      const size = 18 + Math.random() * 46;
      poop.style.width = poop.style.height = size.toFixed(0) + 'px';
      poop.style.left = (left * 100).toFixed(2) + '%';
      poop.style.top = (top * 100).toFixed(2) + '%';
      poop.style.transform = 'rotate(' + (Math.random() * 44 - 22).toFixed(0) + 'deg)';
      poop.style.animationDelay = (Math.random() * 4).toFixed(2) + 's';
      poop.style.opacity = (0.1 + Math.random() * 0.16).toFixed(2);
      if (Math.random() < 0.35) poop.style.background = 'var(--muted)';
      field.append(poop);
    }
  }
  hero.prepend(field);
}

initThemeToggle($('theme-toggle'));
$('time-zone').textContent = '时间按' + browserTimeZoneLabel() + '显示';
poopField();
$('reload').addEventListener('click', loadFeed);
loadFeed();
