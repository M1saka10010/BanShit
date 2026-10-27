export function initModelPicker(root) {
  const input = root.querySelector('input');
  const popup = root.querySelector('.model-popup');
  const list = root.querySelector('[role="listbox"]');
  const empty = root.querySelector('.model-empty');
  let models = [];
  let matches = [];
  let active = -1;

  function close() {
    popup.hidden = true;
    input.setAttribute('aria-expanded', 'false');
    input.removeAttribute('aria-activedescendant');
    active = -1;
  }

  function highlight(index) {
    active = index;
    Array.from(list.children).forEach((option, i) => {
      option.setAttribute('aria-selected', String(i === active));
    });
    const option = list.children[active];
    if (option) {
      input.setAttribute('aria-activedescendant', option.id);
      option.scrollIntoView({ block: 'nearest' });
    } else {
      input.removeAttribute('aria-activedescendant');
    }
  }

  function select(index) {
    input.value = matches[index];
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
    close();
  }

  function open(filter = true) {
    const query = filter ? input.value.trim().toLowerCase() : '';
    matches = models.filter((model) => model.toLowerCase().includes(query));
    list.replaceChildren(...matches.map((model, index) => {
      const option = document.createElement('div');
      option.id = `${list.id}-option-${index}`;
      option.className = 'model-option';
      option.setAttribute('role', 'option');
      option.setAttribute('aria-selected', 'false');
      option.textContent = model;
      option.addEventListener('click', () => select(index));
      return option;
    }));
    empty.hidden = matches.length > 0;
    empty.textContent = models.length
      ? '没有匹配的模型，可继续使用手填 ID。'
      : '尚无可用模型，请获取列表或直接输入 ID。';
    popup.hidden = false;
    input.setAttribute('aria-expanded', 'true');
    highlight(-1);
  }

  input.addEventListener('focus', () => open(false));
  input.addEventListener('click', () => { if (popup.hidden) open(false); });
  input.addEventListener('input', () => open());
  input.addEventListener('blur', close);
  list.addEventListener('mousedown', (event) => event.preventDefault());
  input.addEventListener('keydown', (event) => {
    if (event.isComposing) return;
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      if (popup.hidden) open(false);
      if (matches.length) {
        const next = active < 0
          ? (event.key === 'ArrowDown' ? 0 : matches.length - 1)
          : (active + (event.key === 'ArrowDown' ? 1 : -1) + matches.length) % matches.length;
        highlight(next);
      }
    } else if (event.key === 'Enter' && !popup.hidden) {
      event.preventDefault();
      if (active >= 0) select(active);
      else close();
    } else if (event.key === 'Escape' && !popup.hidden) {
      event.preventDefault();
      close();
    } else if (event.key === 'Tab') {
      close();
    }
  });
  document.addEventListener('pointerdown', (event) => {
    if (!root.contains(event.target)) close();
  });

  return {
    setModels(ids) {
      models = [...new Set(ids)];
      if (!popup.hidden) open();
    },
  };
}
