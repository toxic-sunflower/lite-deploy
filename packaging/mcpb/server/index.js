// Launcher of the Lite Deploy extension: runs the lite-ui program that is installed on this workstation,
// so the assistant always uses the same version as the page you open (update lite-ui, and this follows).
// On Windows, if lite-ui is only inside WSL, it is started through wsl.exe.
// Everything it decides is written to stderr (Claude Desktop shows it in the log of the extension).
'use strict';
const { spawn } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

function say(msg) { process.stderr.write('[launcher] ' + msg + '\n'); }

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

function run(cmd, args, what) {
  say('starting ' + what + ' (server: ' + (server || 'from the list of lite-ui') + ')');
  const child = spawn(cmd, args, { stdio: 'inherit', windowsHide: true });
  child.on('error', (e) => { say('cannot start ' + what + ': ' + e.message); process.exit(1); });
  child.on('exit', (code, signal) => { say(what + ' stopped (code ' + code + (signal ? ', signal ' + signal : '') + ')'); process.exit(code === null ? 1 : code); });
}

if (found) {
  run(found, server ? ['mcp', '--server', server] : ['mcp'], found);
} else if (isWin) {
  // Not on Windows: try WSL. The script says clearly if lite-ui is not there either.
  const root = process.env.SystemRoot || 'C:\\Windows';
  const wsl = [path.join(root, 'System32', 'wsl.exe')].find((p) => fs.existsSync(p)) || 'wsl.exe';
  const script =
    'p=$(command -v lite-ui || echo "$HOME/.local/bin/lite-ui"); ' +
    '[ -x "$p" ] || { echo "lite-ui is not installed in WSL either" >&2; exit 127; }; ' +
    'exec "$p" mcp ${1:+--server "$1"}';
  run(wsl, ['-e', 'sh', '-c', script, 'sh', server], 'lite-ui in WSL');
} else {
  say('lite-ui is not installed. Install it once, then restart Claude Desktop:');
  say('  curl -fsSL https://lite-deploy.prozorov.me/install-ui.sh | sh');
  process.exit(1);
}
