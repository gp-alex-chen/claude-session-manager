import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';
import './styles/style.css';
import * as backend from './api/backend.js';
import { createApplication } from './app/bootstrap.js';

const app = createApplication({
  documentRef: document,
  windowRef: window,
  runtime: window.runtime,
  backend,
  TerminalCtor: Terminal,
  FitAddonCtor: FitAddon,
});

void app.start();
