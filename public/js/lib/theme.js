'use strict';

/* 主题切换：自动（跟随系统）→ 亮色 → 暗色 循环，选择存入 localStorage。 */

const KEY = 'banshit-theme';
const STATES = ['auto', 'light', 'dark'];
const LABELS = { auto: '主题 / 自动', light: '主题 / 亮色', dark: '主题 / 暗色' };

function read() {
  try {
    const value = localStorage.getItem(KEY);
    return value === 'light' || value === 'dark' ? value : 'auto';
  } catch {
    return 'auto';
  }
}

function apply(state) {
  if (state === 'auto') {
    delete document.documentElement.dataset.theme;
  } else {
    document.documentElement.dataset.theme = state;
  }
}

/** 绑定主题切换按钮，返回当前状态。 */
export function initThemeToggle(button) {
  let state = read();
  const render = () => {
    button.textContent = LABELS[state];
    button.title = '点击切换主题（当前：' + LABELS[state].slice(5) + '）';
  };
  button.addEventListener('click', () => {
    state = STATES[(STATES.indexOf(state) + 1) % STATES.length];
    try {
      if (state === 'auto') localStorage.removeItem(KEY);
      else localStorage.setItem(KEY, state);
    } catch { /* 隐私模式下忽略持久化失败 */ }
    apply(state);
    render();
  });
  render();
  return state;
}
