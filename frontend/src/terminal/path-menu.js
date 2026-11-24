export function createPathMenu(documentRef, openPath, setStatus) {
  let active = null;

  function close(session) {
    if (!active || (session && active.session !== session)) return;
    const { menu, outside, escape } = active;
    active = null;
    documentRef.removeEventListener('pointerdown', outside, true);
    documentRef.removeEventListener('keydown', escape, true);
    menu.remove();
  }

  function show(session, event, target) {
    if (event.button !== 0 || !session.visible || session.term.getSelection?.()) return;
    close();

    const menu = documentRef.createElement('div');
    menu.className = 'terminal-path-menu';
    const outside = (event) => {
      if (!menu.contains(event.target)) close();
    };
    const escape = (event) => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      event.stopPropagation?.();
      close();
      session.term.focus();
    };
    for (const [label, action, title] of [
      ['直接打开', 'open', '按系统默认方式打开，可执行文件会运行'],
      ['打开文件夹', 'folder', '打开目录或文件所在目录'],
    ]) {
      const button = documentRef.createElement('button');
      button.type = 'button';
      button.className = 'ctx-item';
      button.textContent = label;
      button.title = title;
      button.addEventListener('click', async () => {
        if (active?.menu !== menu) return;
        close();
        session.term.focus();
        try {
          await openPath(session.dir || '', target, action);
        } catch (error) {
          setStatus?.('打开路径失败：' + (error?.message || error), 'warn');
        }
      });
      menu.appendChild(button);
    }

    active = { session, menu, outside, escape };
    documentRef.body.appendChild(menu);
    const { width, height } = menu.getBoundingClientRect();
    const { clientWidth, clientHeight } = documentRef.documentElement;
    menu.style.left = Math.max(0, Math.min(event.clientX, clientWidth - width)) + 'px';
    menu.style.top = Math.max(0, Math.min(event.clientY, clientHeight - height)) + 'px';
    documentRef.addEventListener('pointerdown', outside, true);
    documentRef.addEventListener('keydown', escape, true);
    menu.children[0].focus({ preventScroll: true });
  }

  return { show, close };
}
