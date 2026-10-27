'use strict';

/* 同源 JSON API 封装：统一错误处理与 401 回调。 */

/**
 * 发起 API 请求并解析 JSON 响应。
 * options.onUnauthorized：收到 401 时调用（如跳转登录视图）。
 */
export async function api(path, method = 'GET', body, options = {}) {
  const init = { method, credentials: 'same-origin', cache: 'no-store' };
  if (body !== undefined) {
    init.headers = { 'Content-Type': 'application/json' };
    init.body = JSON.stringify(body);
  }
  const response = await fetch(path, init);
  if (options.isCurrent && !options.isCurrent()) throw new DOMException('会话已更新', 'AbortError');
  if (response.status === 401) {
    options.onUnauthorized?.();
    throw new Error(path === '/api/login' ? '密码错误，请重试。' : '请先登录，或登录已过期。');
  }
  const text = await response.text();
  if (options.isCurrent && !options.isCurrent()) throw new DOMException('会话已更新', 'AbortError');
  let data = {};
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      if (response.ok) throw new Error('服务器返回格式错误，请重试。');
    }
  }
  if (!response.ok) throw new Error(typeof data.error === 'string' ? data.error : '请求失败（' + response.status + '）');
  return data;
}
