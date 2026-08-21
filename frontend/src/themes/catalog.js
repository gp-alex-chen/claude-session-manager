const themes = {
  claude: {
    name: 'Claude 暖黑',
    foreground: '#e8e6e1', background: '#141412', cursor: '#d97757',
    cursorAccent: '#141412', selectionBackground: 'rgba(217,119,87,.32)', selectionForeground: '#ffffff',
    black: '#1f1e1b', red: '#e58a8a', green: '#8fb996', yellow: '#e0a458',
    blue: '#8ab4d8', magenta: '#c9a7d8', cyan: '#8ad0d0', white: '#e8e6e1',
    brightBlack: '#6f6c66', brightRed: '#f0a09a', brightGreen: '#a8ccae',
    brightYellow: '#eec07a', brightBlue: '#a3c8ea', brightMagenta: '#dcc0ea',
    brightCyan: '#a4e2e2', brightWhite: '#faf8f4',
  },
  dracula: {
    name: 'Dracula',
    foreground: '#f8f8f2', background: '#282a36', cursor: '#f8f8f2',
    cursorAccent: '#282a36', selectionBackground: 'rgba(189,147,249,.3)', selectionForeground: '#ffffff',
    black: '#21222c', red: '#ff5555', green: '#50fa7b', yellow: '#f1fa8c',
    blue: '#bd93f9', magenta: '#ff79c6', cyan: '#8be9fd', white: '#f8f8f2',
    brightBlack: '#6272a4', brightRed: '#ff6e6e', brightGreen: '#69ff94',
    brightYellow: '#ffffa5', brightBlue: '#d6acff', brightMagenta: '#ff92df',
    brightCyan: '#a4ffff', brightWhite: '#ffffff',
  },
  onedark: {
    name: 'One Dark',
    foreground: '#abb2bf', background: '#282c34', cursor: '#528bff',
    cursorAccent: '#282c34', selectionBackground: 'rgba(97,175,239,.3)', selectionForeground: '#ffffff',
    black: '#282c34', red: '#e06c75', green: '#98c379', yellow: '#e5c07b',
    blue: '#61afef', magenta: '#c678dd', cyan: '#56b6c2', white: '#abb2bf',
    brightBlack: '#5c6370', brightRed: '#be5046', brightGreen: '#98c379',
    brightYellow: '#d19a66', brightBlue: '#61afef', brightMagenta: '#c678dd',
    brightCyan: '#56b6c2', brightWhite: '#ffffff',
  },
  solarized: {
    name: 'Solarized Dark',
    foreground: '#839496', background: '#002b36', cursor: '#93a1a1',
    cursorAccent: '#002b36', selectionBackground: 'rgba(38,139,210,.3)', selectionForeground: '#ffffff',
    black: '#073642', red: '#dc322f', green: '#859900', yellow: '#b58900',
    blue: '#268bd2', magenta: '#d33682', cyan: '#2aa198', white: '#eee8d5',
    brightBlack: '#586e75', brightRed: '#cb4b16', brightGreen: '#859900',
    brightYellow: '#b58900', brightBlue: '#268bd2', brightMagenta: '#d33682',
    brightCyan: '#2aa198', brightWhite: '#fdf6e3',
  },
  nord: {
    name: 'Nord',
    foreground: '#d8dee9', background: '#2e3440', cursor: '#d8dee9',
    cursorAccent: '#2e3440', selectionBackground: 'rgba(136,192,208,.3)', selectionForeground: '#ffffff',
    black: '#3b4252', red: '#bf616a', green: '#a3be8c', yellow: '#ebcb8b',
    blue: '#81a1c1', magenta: '#b48ead', cyan: '#88c0d0', white: '#e5e9f0',
    brightBlack: '#4c566a', brightRed: '#bf616a', brightGreen: '#a3be8c',
    brightYellow: '#ebcb8b', brightBlue: '#81a1c1', brightMagenta: '#b48ead',
    brightCyan: '#8fbcbb', brightWhite: '#eceff4',
  },
  solarizedlight: {
    name: 'Solarized Light',
    foreground: '#657b83', background: '#fdf6e3', cursor: '#586e75',
    cursorAccent: '#fdf6e3', selectionBackground: 'rgba(38,139,210,.22)', selectionForeground: '#002b36',
    black: '#073642', red: '#dc322f', green: '#859900', yellow: '#b58900',
    blue: '#268bd2', magenta: '#d33682', cyan: '#2aa198', white: '#eee8d5',
    brightBlack: '#586e75', brightRed: '#cb4b16', brightGreen: '#859900',
    brightYellow: '#b58900', brightBlue: '#268bd2', brightMagenta: '#d33682',
    brightCyan: '#2aa198', brightWhite: '#fdf6e3',
  },
  onelight: {
    name: 'One Light',
    foreground: '#383a42', background: '#fafafa', cursor: '#526eff',
    cursorAccent: '#fafafa', selectionBackground: 'rgba(82,110,255,.2)', selectionForeground: '#383a42',
    black: '#383a42', red: '#e45649', green: '#50a14f', yellow: '#c18401',
    blue: '#0184bc', magenta: '#a626a4', cyan: '#0997b3', white: '#fafafa',
    brightBlack: '#a0a1a7', brightRed: '#e45649', brightGreen: '#50a14f',
    brightYellow: '#c18401', brightBlue: '#0184bc', brightMagenta: '#a626a4',
    brightCyan: '#0997b3', brightWhite: '#fafafa',
  },
  githublight: {
    name: 'GitHub Light',
    foreground: '#1f2328', background: '#ffffff', cursor: '#0969da',
    cursorAccent: '#ffffff', selectionBackground: 'rgba(9,105,218,.2)', selectionForeground: '#1f2328',
    black: '#24292f', red: '#cf222e', green: '#116329', yellow: '#4d2d00',
    blue: '#0969da', magenta: '#8250df', cyan: '#1b7c83', white: '#ffffff',
    brightBlack: '#6e7781', brightRed: '#cf222e', brightGreen: '#116329',
    brightYellow: '#4d2d00', brightBlue: '#0969da', brightMagenta: '#8250df',
    brightCyan: '#1b7c83', brightWhite: '#ffffff',
  },
};

function freezeTheme(theme) {
  return Object.freeze(theme);
}

export const THEMES = Object.freeze(
  Object.fromEntries(Object.entries(themes).map(([name, theme]) => [name, freezeTheme(theme)])),
);

export const TERM_OPTS = Object.freeze({
  fontFamily: "'Cascadia Mono', Consolas, 'Microsoft YaHei', monospace",
  fontSize: 14,
  lineHeight: 1.2,
  cursorBlink: true,
  scrollback: 8000,
});

export function createTermOptions(themeName = 'claude') {
  return {
    ...TERM_OPTS,
    theme: THEMES[themeName] || THEMES.claude,
  };
}
