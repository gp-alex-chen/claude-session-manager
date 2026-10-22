const EVENTS = [
  ['input_requested', '等待回答'],
  ['permission_requested', '等待权限审批'],
  ['task_completed', '任务完成'],
];

export function createNotificationSettings({ backend, el, setIntervalFn = setInterval, clearIntervalFn = clearInterval }) {
  let generation = 0;
  let timer = null;
  const stop = () => { generation++; if (timer !== null) clearIntervalFn(timer); timer = null; };

  async function mount(panel) {
    stop();
    const current = generation;
    const live = () => current === generation;
    let ready = false;
    let busy = false;
    let refreshing = false;
    const field = (key, title, tag = 'input', type = 'text') => {
      const label = el('label', 'notification-field');
      const input = el(tag, 'notification-input');
      input.dataset.field = key;
      input.setAttribute('aria-label', title);
      if (tag === 'input') input.type = type;
      if (tag === 'textarea') { input.rows = 3; input.spellcheck = false; }
      if (type === 'checkbox') { label.className += ' notification-checkbox'; label.append(input, el('span', '', title)); }
      else label.append(el('span', '', title), input);
      panel.appendChild(label); return input;
    };
    panel.appendChild(el('h3', 'settings-section-title', '自定义通知程序'));
    panel.appendChild(el('p', 'settings-section-description',
      '检测到等待回答、权限审批或任务完成时，在后台调用你的程序。通过标准输入传入 UTF-8 JSON，包含会话名称、项目路径和状态；不包含对话或审批正文。'));
    const enabled = field('enabled', '启用自动通知', 'input', 'checkbox');
    const program = field('program', '程序路径或命令名'); program.placeholder = 'python.exe 或 D:\\tools\\notify.exe';
    const args = field('args', '启动参数（JSON 数组）', 'textarea'); args.placeholder = '["D:\\\\scripts\\\\notify.py"]';
    panel.appendChild(el('p', 'settings-note', '每个数组元素是一个参数，保留空格和空字符串，不经过 Shell 解释。例如：["D:\\\\scripts\\\\notify.py", "--channel", "my channel"]。'));
    const dir = field('workingDir', '工作目录（留空使用 TheOne 所在目录）');
    const timeout = field('timeoutSeconds', '执行超时（1–120 秒）', 'input', 'number'); timeout.min = '1'; timeout.max = '120'; timeout.step = '1';
    const eventFields = EVENTS.map(([key, title]) => [key, field(key, title, 'input', 'checkbox')]);
    const testEvent = field('testEvent', '测试事件', 'select');
    for (const [value, text] of EVENTS) { const option = el('option', '', text); option.value = value; testEvent.appendChild(option); }
    testEvent.value = 'permission_requested';
    const actions = el('div', 'notification-actions');
    const save = el('button', 'update-action', '保存配置'); save.type = 'button'; save.dataset.field = 'save';
    const test = el('button', 'update-action', '保存并发送测试事件'); test.type = 'button'; test.dataset.field = 'test';
    actions.append(save, test); panel.appendChild(actions);
    const message = el('p', 'settings-note', '正在加载配置…'); message.dataset.field = 'message'; message.setAttribute('role', 'status'); panel.appendChild(message);
    const result = el('pre', 'notification-result', '尚无调用记录'); result.dataset.field = 'result'; result.setAttribute('aria-live', 'polite'); panel.appendChild(result);
    panel.appendChild(el('p', 'settings-note', '测试使用当前保存的配置，即使未启用自动通知也可执行。调用串行排队，最多等待 32 条；失败不自动重试。修改配置会取消当前调用并清空等待队列。调用结果只保留在内存，不写入应用日志。'));
    const updateButtons = () => { save.disabled = test.disabled = !ready || busy; };
    updateButtons();
    const read = () => {
      let values;
      try { values = JSON.parse(args.value); } catch { throw Error('启动参数须为 JSON 数组，例如 ["notify.py"]'); }
      if (!Array.isArray(values) || values.some(value => typeof value !== 'string')) throw Error('启动参数须为字符串数组');
      const seconds = Number(timeout.value);
      if (!Number.isInteger(seconds) || seconds < 1 || seconds > 120) throw Error('执行超时须为 1–120 秒的整数');
      return { enabled: enabled.checked, program: program.value, args: values, workingDir: dir.value,
        timeoutSeconds: seconds, events: eventFields.filter(([, input]) => input.checked).map(([key]) => key) };
    };
    async function refresh() {
      if (!live() || refreshing) return;
      refreshing = true;
      try {
        const status = await backend.GetNotificationStatus();
        if (!live()) return;
        const lines = [`${status.running ? '正在执行' : '空闲'} · 排队 ${status.pending || 0}/32 · 队列满丢弃 ${status.dropped || 0} 条`];
        const last = status.last;
        if (last) {
          lines.push(`${last.test ? '测试 · ' : ''}${EVENTS.find(([key]) => key === last.event)?.[1] || last.event} · ${last.success ? '成功' : '失败'} · 退出码 ${last.exitCode}`);
          if (last.finishedAt) lines.push(last.finishedAt);
          if (last.error) lines.push(last.error);
        } else lines.push('尚无调用记录');
        result.textContent = lines.join('\n');
      } catch (error) { if (live()) result.textContent = '读取调用结果失败：' + error; }
      finally { refreshing = false; }
    }
    async function submit(testing) {
      if (!ready || busy || !live()) return;
      busy = true; updateButtons();
      try {
        const config = read();
        const selectedTest = testEvent.value;
        await backend.SetNotificationConfig(config);
        if (!live()) return;
        if (testing) await backend.TestNotification(selectedTest);
        if (!live()) return;
        message.textContent = testing ? '测试事件已排队，执行结果见下方。' : '配置已保存。';
        await refresh();
      } catch (error) { if (live()) message.textContent = '操作失败：' + error; }
      finally { busy = false; if (live()) updateButtons(); }
    }
    save.addEventListener('click', () => submit(false)); test.addEventListener('click', () => submit(true));
    try {
      const config = await backend.GetNotificationConfig();
      if (!live()) return;
      enabled.checked = Boolean(config.enabled); program.value = config.program || '';
      args.value = JSON.stringify(config.args || [], null, 2); dir.value = config.workingDir || '';
      timeout.value = String(config.timeoutSeconds);
      for (const [key, input] of eventFields) input.checked = (config.events || []).includes(key);
      ready = true; message.textContent = '修改后保存即可生效。'; updateButtons();
      await refresh();
      if (live()) timer = setIntervalFn(refresh, 1000);
    } catch (error) { if (live()) message.textContent = '无法读取配置：' + error; }
  }
  return { mount, stop };
}
