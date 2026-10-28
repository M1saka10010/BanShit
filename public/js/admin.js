'use strict';

/* 管理后台：登录、配置、运行状态、复制记录，按标签页组织。 */

import { $, node, dateLabel, browserTimeZoneLabel, sourceURL } from './lib/dom.js';
import { api } from './lib/api.js';
import { initThemeToggle } from './lib/theme.js';
import { initModelPicker } from './lib/model-picker.js';
import { initTelegram } from './lib/telegram.js';

const modelPicker = initModelPicker($('model-picker'));

let sessionVersion = 0;
const request = (path, method, body) => {
  const version = sessionVersion;
  return api(path, method, body, {
    onUnauthorized: loginView,
    isCurrent: () => version === sessionVersion,
  });
};

function message(text, error = false) {
  $('message').textContent = text;
  $('message').classList.toggle('error', error);
}

function loginView() {
  sessionVersion++;
  telegram.reset();
  copiesLoading = false;
  copiesSnapshot = undefined;
  $('copy-history').hidden = true;
  $('copies-list').replaceChildren();
  $('dashboard').hidden = true;
  $('logout').hidden = true;
  $('login-panel').hidden = false;
  $('api-key').value = '';
  $('password').focus();
}

/** 按钮忙碌态包装：禁用按钮、捕获错误到通知条。 */
async function busy(button, action) {
  button.disabled = true;
  try {
    await action();
  } catch (error) {
    if (error.name !== 'AbortError') message(error.message || '网络异常，请重试。', true);
  } finally {
    button.disabled = false;
  }
}

/* ---------- 标签页 ---------- */
const tabNames = [
  ['settings', '内容与 AI 配置'],
  ['telegram', 'Telegram 账号'],
  ['status', '运行状态'],
  ['activity', '用户操作'],
];
const panels = {};
let activeTab = 'settings';

function selectTab(name) {
  activeTab = name;
  for (const [key] of tabNames) {
    const selected = key === name;
    panels[key].hidden = !selected;
    $('tab-' + key).setAttribute('aria-selected', String(selected));
    $('tab-' + key).tabIndex = selected ? 0 : -1;
  }
  if (name === 'activity' && !$('dashboard').hidden) {
    $('copy-history').hidden = false;
    void loadCopies(1);
  }
}

function buildTabs() {
  const tabBar = node('div', 'admin-tabs');
  tabBar.setAttribute('role', 'tablist');
  tabBar.setAttribute('aria-label', '后台功能');
  for (const [name, label] of tabNames) {
    const panel = node('section');
    panel.id = 'panel-' + name;
    panel.setAttribute('role', 'tabpanel');
    panel.setAttribute('aria-labelledby', 'tab-' + name);
    panel.tabIndex = 0;
    panels[name] = panel;
    const button = node('button', '', label);
    button.type = 'button';
    button.id = 'tab-' + name;
    button.setAttribute('role', 'tab');
    button.setAttribute('aria-controls', panel.id);
    button.onclick = () => selectTab(name);
    button.onkeydown = (event) => {
      const index = tabNames.findIndex(([key]) => key === name);
      let next;
      if (event.key === 'ArrowRight') next = (index + 1) % tabNames.length;
      else if (event.key === 'ArrowLeft') next = (index + tabNames.length - 1) % tabNames.length;
      else if (event.key === 'Home') next = 0;
      else if (event.key === 'End') next = tabNames.length - 1;
      else return;
      event.preventDefault();
      selectTab(tabNames[next][0]);
      $('tab-' + tabNames[next][0]).focus();
    };
    tabBar.append(button);
  }
  panels.settings.append($('config-form'));
  panels.telegram.append($('telegram-panel'));
  panels.status.append($('stats-panel'), $('operations-panel'));
  panels.activity.append($('copy-history'));
  $('dashboard').replaceChildren(tabBar, ...Object.values(panels));
  selectTab(activeTab);
}

/* ---------- 配置与状态 ---------- */
function renderStatus(data) {
  $('stat-memes').textContent = data.stats.memes;
  $('stat-copies').textContent = data.stats.copies;
  $('stat-running').textContent = data.status.running ? '运行中' : '待命中';
  $('last-run').textContent = dateLabel(data.status.lastRun);
  $('last-optimization').textContent = dateLabel(data.status.lastOptimization);
  $('last-error').textContent = data.status.lastError || '暂无错误，一切正常';
  $('refresh-task').disabled = data.status.running;
  $('reevaluate-task').disabled = data.status.running;
  $('optimize-task').disabled = data.status.running;
}

async function loadConfig() {
  const data = await request('/api/admin/config');
  $('channels').value = data.channels.join('\n');
  $('interval').value = data.intervalMinutes;
  $('optimize-hours').value = data.optimizeEveryHours;
  $('interest').value = data.interestPrompt;
  $('base-url').value = data.ai.baseUrl;
  $('mode').value = data.ai.mode;
  $('model').value = data.ai.model;
  $('concurrency').value = data.ai.concurrency || 8;
  $('api-key').value = '';
  $('key-status').textContent = data.ai.hasApiKey ? '（已配置）' : '（未配置）';
  renderStatus(data);
  $('login-panel').hidden = true;
  $('dashboard').hidden = false;
  $('logout').hidden = false;
  selectTab(activeTab);
}

function bindForms() {
  $('login-form').addEventListener('submit', (event) => {
    event.preventDefault();
    busy(event.submitter, async () => {
      sessionVersion++;
      await request('/api/login', 'POST', { password: $('password').value });
      $('password').value = '';
      await loadConfig();
      message('欢迎回来，配置已加载。');
    });
  });
  $('logout').addEventListener('click', (event) => busy(event.currentTarget, async () => {
    await request('/api/logout', 'POST');
    loginView();
    message('已安全退出登录。');
  }));
  $('config-form').addEventListener('submit', (event) => {
    event.preventDefault();
    busy(event.submitter, async () => {
      const config = {
        channels: $('channels').value.split(/[\n,]+/).map((value) => value.trim()).filter(Boolean),
        intervalMinutes: Number($('interval').value),
        optimizeEveryHours: Number($('optimize-hours').value),
        interestPrompt: $('interest').value,
        ai: {
          baseUrl: $('base-url').value.trim(),
          mode: $('mode').value,
          model: $('model').value.trim(),
          concurrency: Number($('concurrency').value),
          apiKey: $('api-key').value,
        },
      };
      await request('/api/admin/config', 'PUT', config);
      if (config.ai.apiKey) $('key-status').textContent = '（已配置）';
      $('api-key').value = '';
      message('配置已保存。');
    });
  });
  $('load-models').addEventListener('click', (event) => busy(event.currentTarget, async () => {
    $('model-message').textContent = '正在获取模型…';
    try {
      const data = await request('/api/admin/models', 'POST', {
        baseUrl: $('base-url').value.trim(),
        mode: $('mode').value,
        apiKey: $('api-key').value,
      });
      modelPicker.setModels(data.models.map((model) => model.id));
      $('model-message').textContent = data.models.length
        ? '已获取 ' + data.models.length + ' 个模型，点击模型输入框选择。'
        : '没有可用模型，可手动输入模型 ID。';
    } catch (error) {
      if (error.name === 'AbortError') return;
      $('model-message').textContent = '获取失败，可重试或手动输入模型 ID。';
      throw error;
    }
  }));
  for (const [id, endpoint, label] of [
    ['refresh-task', 'refresh', '采集'],
    ['reevaluate-task', 'reevaluate', '重新评估'],
    ['optimize-task', 'optimize', '兴趣优化'],
  ]) {
    $(id).addEventListener('click', (event) => busy(event.currentTarget, async () => {
      if (endpoint === 'reevaluate' && !confirm('重新评估会重新消耗模型额度，并处理当前全部候选。确认继续？')) return;
      await request('/api/admin/' + endpoint, 'POST');
      message(label + '任务已提交，正在后台执行；可点击「更新运行状态」查看进度。');
    }));
  }
  $('reload-status').addEventListener('click', (event) => busy(event.currentTarget, async () => {
    renderStatus(await request('/api/admin/config'));
    message('运行状态已更新，未覆盖未保存的配置。');
  }));
}

/* ---------- 复制记录 ---------- */
let copiesPage = 1, copiesPages = 0;
let copiesFilters = {};
let copiesLoading = false;
let copiesSnapshot;

function restoreCopiesFilters() {
  for (const key of ['start', 'end']) {
    const date = copiesFilters[key] ? new Date(copiesFilters[key]) : null;
    $('copies-' + key).value = date
      ? new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 19)
      : '';
  }
}

function copyRecordNode(item) {
  const row = node('article', 'copy-record');
  const type = item.kind === 'image'
    ? '复制第 ' + (item.imageIndex + 1) + ' 张 / 共 ' + item.imageCount + ' 张'
    : item.kind === 'album'
      ? '复制整组（' + item.imageCount + ' 张）'
      : '复制文字';
  row.append(
    node('p', 'copy-record-title', type),
    node('p', 'hint', dateLabel(item.createdAt) + ' · ' + item.visitor + ' · ' + item.channel + ' · #' + item.id),
    node('p', 'meme-text', item.text || '（无配文）'),
  );
  const pictures = node('div', 'copy-record-images');
  item.images.forEach((src, index) => {
    if (!/^\/media\/[a-f0-9]{64}[.](jpeg|png|webp)$/.test(src)) return;
    const image = document.createElement('img');
    image.src = src + '/thumb';
    image.alt = '复制图片 ' + (index + 1);
    image.loading = 'lazy';
    pictures.append(image);
  });
  row.append(pictures);
  const href = sourceURL(item.url);
  if (href) {
    const link = node('a', 'source-link', '查看原帖 ↗');
    link.href = href;
    link.target = '_blank';
    link.rel = 'noopener noreferrer';
    row.append(link);
  }
  return row;
}

async function loadCopies(page = 1, filters = copiesFilters, snapshot) {
  if (copiesLoading) return;
  const version = sessionVersion;
  copiesLoading = true;
  for (const id of ['copies-refresh', 'copies-prev', 'copies-next','copies-go','copies-apply','copies-reset']) $(id).disabled = true;
  $('copies-state').textContent = '加载中…';
  try {
    const params = new URLSearchParams({...filters,page:String(page)});
    if (Number.isSafeInteger(snapshot) && snapshot > 0) params.set('snapshot', String(snapshot));
    const data = await request('/api/admin/copies?' + params);
    if ($('copy-history').hidden) return;
    const records = data.items.map(copyRecordNode);
    $('copies-list').replaceChildren(...records);
    copiesPage=data.page;copiesPages=data.totalPages;copiesFilters={...filters};
    copiesSnapshot = data.snapshot;
    restoreCopiesFilters();
    $("copies-page").value=copiesPage;$("copies-page").max=Math.max(1,copiesPages);
    $('copies-state').textContent = records.length
      ? '第 ' + copiesPage + ' / ' + copiesPages + ' 页 · 共 ' + data.total + ' 条 · 每页 ' + data.pageSize + ' 条'
      : '共 0 条记录，当前时间范围内还没人动手搬。';
  } catch (error) {
    if (version !== sessionVersion || error.name === 'AbortError') return;
    restoreCopiesFilters();
    $('copies-page').value = copiesPage;
    $('copies-state').textContent = '加载失败：' + error.message;
  } finally {
    if (version !== sessionVersion) return;
    copiesLoading = false;
    $('copies-refresh').disabled = false;
    $('copies-prev').disabled = copiesPage<=1;
    $('copies-next').disabled = copiesPage>=copiesPages;
    $("copies-go").disabled=!copiesPages;$("copies-apply").disabled=false;$("copies-reset").disabled=false;
  }
}

function bindCopies() {
  $('copies-refresh').onclick = () => loadCopies(copiesPage);
  $('copies-next').onclick = () => {
    if (copiesPage<copiesPages) loadCopies(copiesPage+1, copiesFilters, copiesSnapshot);
  };
  $('copies-prev').onclick = () => {
    if (copiesPage>1) loadCopies(copiesPage-1, copiesFilters, copiesSnapshot);
  };
  $("copies-jump").onsubmit=event=>{event.preventDefault();const page=Number($("copies-page").value);if(Number.isInteger(page)&&page>=1&&page<=copiesPages)void loadCopies(page, copiesFilters, copiesSnapshot);};
  $("copies-filter").onsubmit=event=>{
    event.preventDefault();const filters={};
    for(const key of ["start","end"]){const raw=$("copies-"+key).value;if(raw){const date=new Date(raw);if(Number.isNaN(date.getTime())){ $("copies-state").textContent="时间格式无效";restoreCopiesFilters();return;}filters[key]=date.toISOString();}}
    if(filters.start&&filters.end&&filters.start>filters.end){$("copies-state").textContent="开始时间不能晚于结束时间";restoreCopiesFilters();return;}
    void loadCopies(1,filters);
  };
  $("copies-reset").onclick=()=>{$("copies-start").value="";$("copies-end").value="";void loadCopies(1,{});};
}

/* ---------- 启动 ---------- */
const telegram = initTelegram(request);
initThemeToggle($('theme-toggle'));
$('status-time-zone').textContent = '时间按' + browserTimeZoneLabel() + '显示';
$('copies-time-zone').textContent = '记录时间与筛选均使用' + browserTimeZoneLabel();
buildTabs();
bindForms();
bindCopies();
loadConfig()
  .then(() => message('配置已加载，可以开始管理。'))
  .catch((error) => {
    if (error.name === 'AbortError') return;
    if ($('dashboard').hidden) loginView();
    message(error.message || '网络异常，请重试。', true);
  });
