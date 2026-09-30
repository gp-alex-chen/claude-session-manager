// Render the small Markdown subset used by our release notes with DOM nodes.
// Release text never becomes HTML, including unsupported Markdown or tags.
function appendInline(parent, source, el) {
  const token = /\[([^\]]+)\]\((https:\/\/[^\s)]+)\)|`([^`]+)`|\*\*([^*]+)\*\*/g;
  let offset = 0;
  for (const match of source.matchAll(token)) {
    if (match.index > offset) parent.appendChild(el('span', '', source.slice(offset, match.index)));
    if (match[1] !== undefined) {
      try {
        const url = new URL(match[2]);
        if (url.protocol !== 'https:' || url.username || url.password) throw new Error('unsafe URL');
        const link = el('a', 'update-release-link', match[1]);
        link.setAttribute('href', url.href);
        link.setAttribute('target', '_blank');
        link.setAttribute('rel', 'noopener noreferrer');
        parent.appendChild(link);
      } catch {
        parent.appendChild(el('span', '', match[0]));
      }
    } else if (match[3] !== undefined) {
      parent.appendChild(el('code', 'update-release-code', match[3]));
    } else {
      parent.appendChild(el('strong', '', match[4]));
    }
    offset = match.index + match[0].length;
  }
  if (offset < source.length) parent.appendChild(el('span', '', source.slice(offset)));
}

export function renderReleaseNotes(container, source, el) {
  const body = String(source || '').replace(/\r\n?/g, '\n').trim();
  if (!body) {
    container.replaceChildren(el('p', '', '此版本未提供更新说明。'));
    return;
  }

  const blocks = [];
  let list = null;
  let paragraph = null;
  for (const line of body.split('\n')) {
    const value = line.trim();
    if (!value) {
      list = null;
      paragraph = null;
      continue;
    }
    const heading = /^(#{1,6})\s+(.+)$/.exec(value);
    if (heading) {
      list = null;
      paragraph = null;
      const node = el(heading[1].length <= 2 ? 'h5' : 'h6', 'update-release-heading');
      appendInline(node, heading[2], el);
      blocks.push(node);
      continue;
    }
    const item = /^[-*]\s+(.+)$/.exec(value);
    if (item) {
      paragraph = null;
      if (!list) {
        list = el('ul', 'update-release-list');
        blocks.push(list);
      }
      const node = el('li', '');
      appendInline(node, item[1], el);
      list.appendChild(node);
      continue;
    }
    list = null;
    if (!paragraph) {
      paragraph = el('p', 'update-release-paragraph');
      blocks.push(paragraph);
    } else {
      paragraph.appendChild(el('br', ''));
    }
    appendInline(paragraph, value, el);
  }
  container.replaceChildren(...blocks);
}
