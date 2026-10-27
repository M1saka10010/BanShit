/* 首屏渲染前应用已保存的主题，避免明暗闪烁。可选值：light / dark；缺省为跟随系统。 */
try {
  var savedTheme = localStorage.getItem('banshit-theme');
  if (savedTheme === 'light' || savedTheme === 'dark') {
    document.documentElement.dataset.theme = savedTheme;
  }
} catch (error) {}
