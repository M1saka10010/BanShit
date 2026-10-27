'use strict';

import { $ } from './dom.js';

export function initTelegram(request) {
  const root = $('telegram-panel');
  let status = null;
  let pending = false;
  let generation = 0;
  let resendTimer;
  let resendDeadline = 0;
  function resendControls() {
    const waiting = status && ['code', 'unsupported'].includes(status.state);
    const seconds = Math.max(0, Math.ceil((resendDeadline - Date.now()) / 1000));
    const supported = status && ['sms', 'call'].includes(status.nextDelivery) && (status.canResend || status.resendAfter > 0);
    $('tg-resend').hidden = !waiting;
    $('tg-resend').disabled = pending || !waiting || !supported || seconds > 0;
    $('tg-resend').textContent = !supported ? 'Telegram 未提供可用的重发方式' : seconds > 0 ? '重发需等待 ' + seconds + ' 秒' : '重新发送验证码';
  }
  const secrets = ['tg-api-hash', 'tg-phone', 'tg-code', 'tg-password'];
  const clearSecrets = () => secrets.forEach((id) => { $(id).value = ''; });
  function notice(text, error = false) {
    $('tg-message').textContent = text;
    $('tg-message').classList.toggle('error', error);
  }
  function controls() {
    root.querySelectorAll('input, button').forEach((control) => {
      control.disabled = pending || (!status && !['tg-reload', 'tg-reset'].includes(control.id));
    });
    $('tg-collect').disabled = pending || !status || (status.enabled && !status.authorized);
    $('tg-logout').disabled = pending || !status;
    root.setAttribute('aria-busy', String(pending));
    resendControls();
  }
  function render(data, syncConfig = false) {
    status = data;
    clearInterval(resendTimer);
    resendDeadline = Date.now() + Math.max(0, data.resendAfter || 0) * 1000;
    if (['code', 'unsupported'].includes(data.state)) resendTimer = setInterval(resendControls, 1000);
    const deliveryVisible = !data.authorized && Boolean(data.deliveryMessage);
    $('tg-delivery').hidden = !deliveryVisible;
    $('tg-delivery').textContent = deliveryVisible ? data.deliveryMessage : '';
    $('tg-code').placeholder = data.codeLength ? data.codeLength + ' 位数字验证码' : '输入数字验证码';
    if (syncConfig) {
      $('tg-enabled').checked = data.enabled;
      $('tg-api-id').value = data.apiId || '';
    }
    $('tg-hash-status').textContent = data.hasApiHash ? '（已配置）' : '（未配置）';
    const state = data.authorized ? 'authorized' : data.state;
    const labels = { idle: '尚未登录', code: '等待验证码', password: '等待两步验证密码', authorized: '账号已就绪', connecting: '正在连接，请手动更新状态', error: '连接异常，请更新状态或退出会话后重试' };
    $('tg-state').textContent = state === 'unsupported' ? '当前验证方式暂不支持' : labels[state] || '未知状态，请手动更新状态';
    $('tg-source').textContent = !data.enabled
      ? '当前使用网页采集。启用账号采集需保存左侧配置。'
      : data.authorized ? '账号采集已就绪，可以点击「立即采集」。' : '账号采集已启用，请先完成登录。';
    $('tg-phone-form').hidden = state !== 'idle' || !data.apiId || !data.hasApiHash;
    $('tg-code-form').hidden = state !== 'code';
    $('tg-password-form').hidden = state !== 'password';
    controls();
  }
  async function run(action, success, clear = [], syncConfig = false) {
    if (pending) return;
    pending = true;
    const version = generation;
    controls();
    notice('正在处理，请稍候…');
    try {
      const data = await action();
      if (version !== generation) return;
      if (data) render(data, syncConfig);
      notice(data?.error || (data?.authorized ? '账号已就绪。' : data?.deliveryMessage) || success, Boolean(data?.error));
    } catch (error) {
      if (version === generation && error.name !== 'AbortError') notice(error.message || '网络异常，请重试。', true);
    } finally {
      if (version === generation) {
        clear.forEach((id) => { $(id).value = ''; });
        pending = false;
        controls();
      }
    }
  }
  // 手动刷新保留未保存的应用凭据，只在首次加载时填充。
  $('tg-reload').onclick = () => run(() => request('/api/admin/telegram'), '账号状态已更新。', [], !status);
  $('tg-config').onsubmit = (event) => {
    event.preventDefault();
    if (pending || !status) return;
    const apiId = Number($('tg-api-id').value);
    const apiHash = $('tg-api-hash').value.trim();
    const enabled = $('tg-enabled').checked;
    if (!Number.isSafeInteger(apiId) || apiId < 0 || (enabled && !apiId)) {
      notice('请填写有效的 api_id。', true);
      return;
    }
    if ((apiId !== status.apiId || (enabled && !status.hasApiHash)) && !apiHash) {
      notice('首次配置或更换 api_id 时，请填写新的 api_hash。', true);
      return;
    }
    void run(() => request('/api/admin/telegram/config', 'PUT', { enabled, apiId, apiHash }), '账号配置已保存。', secrets, true);
  };
  for (const [form, field, endpoint, key, label] of [
    ['tg-phone-form', 'tg-phone', 'send-code', 'phone', '登录请求已处理，请查看当前状态。'],
    ['tg-code-form', 'tg-code', 'sign-in', 'code', '验证码已提交，请按当前状态继续。'],
    ['tg-password-form', 'tg-password', 'password', 'password', '密码已验证。'],
  ]) {
    $(form).onsubmit = (event) => {
      event.preventDefault();
      if (pending || !status || $(form).hidden) return;
      const value = key === 'password' ? $(field).value : $(field).value.trim();
      if (!value) { notice('请先填写登录信息。', true); return; }
      void run(() => request('/api/admin/telegram/' + endpoint, 'POST', { [key]: value }), label, secrets);
    };
  }
  $('tg-logout').onclick = () => {
    if (pending || !status || !window.confirm('仅退出本应用的 Telegram 会话，不会退出其他设备。确定继续吗？')) return;
    clearSecrets();
    void run(() => request('/api/admin/telegram/logout', 'POST', {}), '已退出本应用的 Telegram 会话。', secrets);
  };
  $('tg-resend').onclick = () => {
    if (pending || $('tg-resend').disabled) return;
    const version = generation;
    void run(async () => {
      try { return await request('/api/admin/telegram/resend-code', 'POST', {}); }
      catch (error) {
        if (error.name !== 'AbortError') {
          try { const data = await request('/api/admin/telegram'); if (version === generation) render(data); } catch { /* Keep original error. */ }
        }
        throw error;
      }
    }, '重发请求已处理，请查看投递方式。', ['tg-code']);
  };
  $('tg-reset').onclick = () => {
    if (pending || !window.confirm('仅归档本地会话以重新配置，不会撤销 Telegram 远端授权；如会话泄露请先在 Telegram 客户端「设备」中终止。确定重置本机会话吗？')) return;
    clearSecrets();
    void run(() => request('/api/admin/telegram/reset', 'POST', { confirm: true }), '本机会话已归档，可修正 API 凭据后重新登录。', secrets, true);
  };
  $('tg-collect').onclick = () => {
    if (pending || !status || (status.enabled && !status.authorized)) return;
    void run(async () => {
      await request('/api/admin/refresh', 'POST');
    }, '采集任务已提交，可在「运行状态」中手动更新进度。');
  };
  function reset() {
    clearInterval(resendTimer);
    resendDeadline = 0;
    $('tg-delivery').hidden = true;
    $('tg-delivery').textContent = '';
    generation++;
    pending = false;
    status = null;
    clearSecrets();
    $('tg-enabled').checked = false;
    $('tg-api-id').value = '';
    $('tg-hash-status').textContent = '';
    $('tg-state').textContent = '状态尚未更新';
    $('tg-source').textContent = '请先手动更新账号状态。';
    for (const id of ['tg-phone-form', 'tg-code-form', 'tg-password-form']) $(id).hidden = true;
    notice('点击「更新账号状态」开始管理；不会自动发送验证码。');
    controls();
  }
  reset();
  return { reset };
}
