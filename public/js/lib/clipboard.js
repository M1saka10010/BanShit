'use strict';

/* 剪贴板复制：文字、单图转 PNG、整组纵向拼图，成功后上报服务端统计。 */

import { notify } from './dom.js';

const MAX_WIDTH = 8000;
const MAX_HEIGHT = 16000;
const MAX_PIXELS = 24000000;

export function isImageData(value) {
  return typeof value === 'string' && /^data:image\/(png|jpe?g|gif|webp|avif|bmp);base64,/i.test(value);
}

/** 将 Data URL 图片解码并重新编码为 PNG Blob，附带尺寸安全上限。 */
export async function pngBlob(dataURL) {
  if (!isImageData(dataURL)) throw new Error('图片格式不可用');
  const response = await fetch(dataURL);
  const blob = await response.blob();
  const objectURL = URL.createObjectURL(blob);
  try {
    const image = new Image();
    image.src = objectURL;
    await image.decode();
    if (image.naturalWidth > MAX_WIDTH || image.naturalHeight > MAX_HEIGHT || image.naturalWidth * image.naturalHeight > MAX_PIXELS) {
      throw new Error('图片尺寸过大，无法安全复制');
    }
    const canvas = document.createElement('canvas');
    canvas.width = image.naturalWidth;
    canvas.height = image.naturalHeight;
    const context = canvas.getContext('2d');
    if (!context) throw new Error('浏览器无法转换图片');
    context.drawImage(image, 0, 0);
    return await new Promise((resolve, reject) => canvas.toBlob(
      (png) => (png ? resolve(png) : reject(new Error('图片转换失败'))), 'image/png'));
  } finally {
    URL.revokeObjectURL(objectURL);
  }
}

/** 将整组图片纵向拼接为一张 PNG Blob。 */
export async function albumBlob(images) {
  const decoded = [];
  try {
    let width = 0;
    let height = 0;
    let pixels = 0;
    for (const data of images) {
      const bitmap = await createImageBitmap(await pngBlob(data));
      decoded.push(bitmap);
      width = Math.max(width, bitmap.width);
      height += bitmap.height;
      pixels += bitmap.width * bitmap.height;
      if (pixels > MAX_PIXELS || height > MAX_HEIGHT || width > MAX_WIDTH) {
        throw new Error('整组图片过大，请逐张复制');
      }
    }
    if (width * height > MAX_PIXELS) throw new Error('拼图尺寸过大，请逐张复制');
    const canvas = document.createElement('canvas');
    canvas.width = width;
    canvas.height = height;
    const context = canvas.getContext('2d');
    if (!context) throw new Error('无法创建拼图');
    context.fillStyle = '#ffffff';
    context.fillRect(0, 0, width, height);
    let y = 0;
    for (const bitmap of decoded) {
      context.drawImage(bitmap, (width - bitmap.width) / 2, y);
      y += bitmap.height;
    }
    return await new Promise((resolve, reject) => canvas.toBlob(
      (png) => (png ? resolve(png) : reject(new Error('拼图失败'))), 'image/png'));
  } finally {
    decoded.forEach((bitmap) => bitmap.close());
  }
}

/**
 * 复制 meme 内容（text / image / album），成功后上报复制记录。
 * 复制失败不记录次数；上报失败不影响已复制的结果。
 */
export async function copyMeme(meme, kind, button, imageIndex = 0) {
  button.disabled = true;
  try {
    if (!window.isSecureContext || !navigator.clipboard) throw new Error('请在 HTTPS 或 localhost 中使用剪贴板');
    if (kind === 'text') {
      await navigator.clipboard.writeText(meme.text);
    } else {
      if (!window.ClipboardItem || !navigator.clipboard.write) throw new Error('此浏览器不支持图片复制');
      // 在点击手势内发起剪贴板请求，PNG 转换异步进行。
      const png = kind === 'album' ? albumBlob(meme.images) : pngBlob(meme.images[imageIndex]);
      png.catch(() => {});
      await navigator.clipboard.write([new ClipboardItem({ 'image/png': png })]);
    }
  } catch (error) {
    notify('复制失败，未记录次数。' + (error.message || '请检查剪贴板权限'));
    button.disabled = false;
    return;
  }
  try {
    const response = await fetch('/api/memes/' + encodeURIComponent(meme.id) + '/copy', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(kind === 'image' ? { kind, imageIndex } : { kind }),
    });
    if (!response.ok) throw new Error('记录失败');
    notify(kind === 'text' ? '文字已搬走，快乐带走！' : '图片已搬走，快去分享吧！💩');
  } catch {
    notify('已搬走，但搬运统计没能记上。');
  } finally {
    button.disabled = false;
  }
}
