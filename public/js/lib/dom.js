'use strict';

/* DOM 与展示层共享工具：选择器、节点工厂、通知、时间与链接格式化。 */

export const $ = (id) => document.getElementById(id);

/** 创建带类名与文本内容的元素。 */
export function node(tag, className, text) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
}

let toastTimer;

/** 底部浮出通知，4.5 秒后自动隐藏。模态对话框打开时移入其中，避免被顶层遮挡。 */
export function notify(message) {
  const toast = $('toast');
  const dialog = document.querySelector('dialog.gallery-dialog[open]');
  if (dialog && toast.parentNode !== dialog) dialog.appendChild(toast);
  if (!dialog && toast.parentNode !== document.body) document.body.appendChild(toast);
  toast.textContent = message;
  toast.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    toast.hidden = true;
  }, 4500);
}

/** 将 ISO 时间格式化为本地时间，无效值返回占位文案。 */
export function dateLabel(value) {
  const date = new Date(value);
  return value && !Number.isNaN(date.getTime()) ? date.toLocaleString('zh-CN') : '暂无记录';
}

/** 校验并返回 http(s) 链接，非法或缺失时返回 null。 */
export function sourceURL(value) {
  try {
    const url = new URL(value);
    return ['http:', 'https:'].includes(url.protocol) ? url.href : null;
  } catch {
    return null;
  }
}
