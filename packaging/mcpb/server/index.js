// Launcher of the Lite Deploy extension: runs the lite-ui program that is installed on this workstation,
// so the assistant always uses the same version as the page you open (update lite-ui, and this follows).
'use strict';
const { spawn, spawnSync } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

// An optional setting that is empty may arrive empty or as the unreplaced placeholder.
let server = process.argv[2] || '';
if (server.startsWith('${')) server = '';
const exe = process.platform === 'win32' ? 'lite-ui.exe' : 'lite-ui';
const home = os.homedir();
const candidates = [process.env.LITE_UI_BIN];
if (process.platform === 'win32') {
  if (process.env.LOCALAPPDATA) candidates.push(path.join(process.env.LOCALAPPDATA, 'lite-ui', exe));
} else {
  candidates.push(path.join(home, '.local', 'bin', exe), '/usr/local/bin/' + exe, '/opt/homebrew/bin/' + exe);
}
for (const d of (process.env.PATH || '').split(path.delimiter)) if (d) candidates.push(path.join(d, exe));

const found = candidates.find((p) => p && fs.existsSync(p));

// Claude Desktop on Windows, lite-ui in WSL: run it through wsl.exe (its keys and ssh are the ones of WSL).
function inWsl() {
  if (process.platform !== 'win32') return false;
  const r = spawnSync('wsl.exe', ['-e', 'sh', '-c', 'test -x "$HOME/.local/bin/lite-ui" || command -v lite-ui >/dev/null 2>&1'], { windowsHide: true, timeout: 20000 });
  return r.status === 0;
}

if (!found && inWsl()) {
  const script = 'p=$(command -v lite-ui || echo "$HOME/.local/bin/lite-ui"); exec "$p" mcp ${1:+--server "$1"}';
  const w = spawn('wsl.exe', ['-e', 'sh', '-c', script, 'sh', server], { stdio: 'inherit', windowsHide: true });
  w.on('error', (e) => { process.stderr.write('Cannot start WSL: ' + e.message + '\n'); process.exit(1); });
  w.on('exit', (code) => process.exit(code === null ? 1 : code));
} else if (!found) {
  process.stderr.write(
    'Lite Deploy is not installed on this workstation.\n' +
    'Install it once (on Windows, or inside WSL), then restart Claude Desktop:\n' +
    '  Windows (PowerShell): irm https://lite-deploy.prozorov.me/install-ui.ps1 | iex\n' +
    '  macOS / Linux:        curl -fsSL https://lite-deploy.prozorov.me/install-ui.sh | sh\n');
  process.exit(1);
}
else {
const child = spawn(found, server ? ['mcp', '--server', server] : ['mcp'], { stdio: 'inherit', windowsHide: true });
child.on('error', (e) => { process.stderr.write('Cannot start ' + found + ': ' + e.message + '\n'); process.exit(1); });
child.on('exit', (code) => process.exit(code === null ? 1 : code));
}
