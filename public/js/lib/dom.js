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
  // 多层对话框（预览框与全屏查看）同时打开时，通知跟随最顶层。
  const openDialogs = document.querySelectorAll('dialog.gallery-dialog[open]');
  const dialog = openDialogs[openDialogs.length - 1];
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
  return value && !Number.isNaN(date.getTime()) ? new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(date) : '暂无记录';
}

export function browserTimeZoneLabel() {
  return '浏览器本地时区（' + new Intl.DateTimeFormat().resolvedOptions().timeZone + '）';
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
