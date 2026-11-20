// Launcher of the Lite Deploy extension: runs the lite-ui program that is installed on this workstation,
// so the assistant always uses the same version as the page you open (update lite-ui, and this follows).
// On Windows, if lite-ui is only inside WSL, it is started through wsl.exe.
// Everything it decides is written to stderr (Claude Desktop shows it in the log of the extension).
'use strict';
const { spawn } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

// Messages go to stderr (the log of the extension in Claude Desktop) and to a small file in the home folder, which is
// rewritten at every start: if the extension fails, that file says why.
let logFile = '';
try { logFile = path.join(os.homedir(), 'lite-deploy-launcher.log'); fs.writeFileSync(logFile, ''); } catch (e) { logFile = ''; }
function say(msg) {
  const line = '[launcher] ' + msg + '\n';
  process.stderr.write(line);
  if (logFile) { try { fs.appendFileSync(logFile, new Date().toISOString() + ' ' + line); } catch (e) { /* no log file */ } }
}
process.on('uncaughtException', (e) => { say('crashed: ' + (e && e.stack || e)); process.exit(1); });

// An optional setting that is empty may arrive empty or as the unreplaced placeholder.
let server = process.argv[2] || '';
if (server.startsWith('${')) server = '';

const isWin = process.platform === 'win32';
const exe = isWin ? 'lite-ui.exe' : 'lite-ui';
const candidates = [process.env.LITE_UI_BIN];
if (isWin) {
  if (process.env.LOCALAPPDATA) candidates.push(path.join(process.env.LOCALAPPDATA, 'lite-ui', exe));
} else {
  const home = os.homedir();
  candidates.push(path.join(home, '.local', 'bin', exe), '/usr/local/bin/' + exe, '/opt/homebrew/bin/' + exe);
}
for (const d of (process.env.PATH || '').split(path.delimiter)) if (d) candidates.push(path.join(d, exe));
const found = candidates.find((p) => p && fs.existsSync(p));

// The program gets its own pipes and the launcher carries the data across: handing the pipes of Claude Desktop over directly
// (stdio: 'inherit') can leave a Windows program with a standard input it cannot read, and it exits at once.
function run(cmd, args, what) {
  say('starting ' + what + ' (server: ' + (server || 'from the list of lite-ui') + ')');
  const child = spawn(cmd, args, { stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true });
  child.on('error', (e) => { say('cannot start ' + what + ': ' + e.message); process.exit(1); });
  child.stdout.on('data', (d) => process.stdout.write(d));
  child.stderr.on('data', (d) => process.stderr.write(d));
  process.stdin.on('data', (d) => { try { child.stdin.write(d); } catch (e) { /* the program is gone */ } });
  process.stdin.on('end', () => { try { child.stdin.end(); } catch (e) { /* the program is gone */ } });
  child.stdin.on('error', () => { /* the program stopped: its exit is reported below */ });
  child.on('exit', (code, signal) => {
    say(what + ' stopped (code ' + code + (signal ? ', signal ' + signal : '') + ')');
    setTimeout(() => process.exit(code === null ? 1 : code), 50); // let the last output through
  });
}

const INSTALL_TEXT =
  'Lite Deploy is not installed on this workstation, so there is nothing to manage servers with yet.\n' +
  'Tell the person to install it once, then restart Claude Desktop:\n' +
  '  Windows (PowerShell):  irm https://lite-deploy.prozorov.me/install-ui.ps1 | iex\n' +
  '  macOS / Linux / WSL:   curl -fsSL https://lite-deploy.prozorov.me/install-ui.sh | sh\n' +
  'Details: https://lite-deploy.prozorov.me/quickstart.html';

// Without lite-ui the extension still starts and says what to do, in the chat: a tiny MCP server with one tool.
function stub(why) {
  say(why);
  const send = (o) => process.stdout.write(JSON.stringify(o) + '\n');
  const tool = {
    name: 'install_lite_ui', description: 'Lite Deploy is not installed on this workstation. Call this to see how to install it.',
    inputSchema: { type: 'object', properties: {}, additionalProperties: false }, annotations: { readOnlyHint: true },
  };
  let buf = '';
  process.stdin.setEncoding('utf8');
  process.stdin.on('data', (chunk) => {
    buf += chunk;
    let i;
    while ((i = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, i).trim();
      buf = buf.slice(i + 1);
      if (!line) continue;
      let m;
      try { m = JSON.parse(line); } catch (e) { continue; }
      if (m.id === undefined) continue; // notifications
      if (m.method === 'initialize') {
        send({ jsonrpc: '2.0', id: m.id, result: { protocolVersion: (m.params && m.params.protocolVersion) || '2024-11-05', capabilities: { tools: {} },
          serverInfo: { name: 'lite-deploy', version: 'not-installed' }, instructions: INSTALL_TEXT } });
      } else if (m.method === 'tools/list') {
        send({ jsonrpc: '2.0', id: m.id, result: { tools: [tool] } });
      } else if (m.method === 'tools/call') {
        send({ jsonrpc: '2.0', id: m.id, result: { content: [{ type: 'text', text: INSTALL_TEXT }] } });
      } else if (m.method === 'ping') {
        send({ jsonrpc: '2.0', id: m.id, result: {} });
      } else {
        send({ jsonrpc: '2.0', id: m.id, error: { code: -32601, message: 'method not found' } });
      }
    }
  });
  process.stdin.on('end', () => process.exit(0));
}

// Is lite-ui inside WSL? Asked without blocking, with a time limit.
function probeWsl(wsl, cb) {
  let done = false;
  const finish = (ok) => { if (!done) { done = true; cb(ok); } };
  const p = spawn(wsl, ['-e', 'sh', '-c', 'test -x "$HOME/.local/bin/lite-ui" || command -v lite-ui >/dev/null 2>&1'], { windowsHide: true, stdio: 'ignore' });
  const timer = setTimeout(() => { try { p.kill(); } catch (e) { /* gone */ } finish(false); }, 20000);
  p.on('error', () => { clearTimeout(timer); finish(false); });
  p.on('exit', (code) => { clearTimeout(timer); finish(code === 0); });
}

if (found && !process.env.LITE_UI_NONE) {
  run(found, server ? ['mcp', '--server', server] : ['mcp'], found);
} else if (isWin) {
  const root = process.env.SystemRoot || 'C:\\Windows';
  const wsl = [path.join(root, 'System32', 'wsl.exe')].find((p) => fs.existsSync(p)) || 'wsl.exe';
  probeWsl(wsl, (ok) => {
    if (!ok || process.env.LITE_UI_NONE) { stub('lite-ui was not found on Windows or in WSL'); return; }
    const script = 'p=$(command -v lite-ui || echo "$HOME/.local/bin/lite-ui"); exec "$p" mcp ${1:+--server "$1"}';
    run(wsl, ['-e', 'sh', '-c', script, 'sh', server], 'lite-ui in WSL');
  });
} else {
  stub('lite-ui was not found');
}
