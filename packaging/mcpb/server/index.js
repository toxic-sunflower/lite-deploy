// Launcher of the Lite Deploy extension: runs the lite-ui program that is installed on this workstation,
// so the assistant always uses the same version as the page you open (update lite-ui, and this follows).
'use strict';
const { spawn, spawnSync } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const server = process.argv[2] || '';
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
if (!found) {
  process.stderr.write(
    'Lite Deploy is not installed on this workstation.\n' +
    'Install it once, then restart Claude Desktop:\n' +
    '  Windows (PowerShell): irm https://lite-deploy.prozorov.me/install-ui.ps1 | iex\n' +
    '  macOS / Linux:        curl -fsSL https://lite-deploy.prozorov.me/install-ui.sh | sh\n');
  process.exit(1);
}
if (!server) {
  process.stderr.write('No server given: fill in "Your server" (user@address) in the extension settings.\n');
  process.exit(1);
}
const child = spawn(found, ['mcp', '--server', server], { stdio: 'inherit', windowsHide: true });
child.on('error', (e) => { process.stderr.write('Cannot start ' + found + ': ' + e.message + '\n'); process.exit(1); });
child.on('exit', (code) => process.exit(code === null ? 1 : code));
