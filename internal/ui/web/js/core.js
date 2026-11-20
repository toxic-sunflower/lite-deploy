(function () {
  var LU = window.LU = { components: {} };
  // an address may carry ?theme=light|dark (used to take the pictures of the documentation); read before the address is cleaned
  var asked = new URLSearchParams(location.search).get('theme');
  var token = new URLSearchParams(location.search).get('t') || sessionStorage.getItem('t') || '';
  if (token) { sessionStorage.setItem('t', token); history.replaceState(null, '', location.pathname + location.hash); }
  var saved = '';
  try { saved = localStorage.getItem('lite-ui-server') || ''; } catch (e) {}

  // theme: light, dark, or the one the system uses (auto); chosen once, kept in the browser
  var theme = 'auto';
  try { theme = localStorage.getItem('lite-ui-theme') || 'auto'; } catch (e) {}
  if (asked === 'light' || asked === 'dark') theme = asked;
  function applyTheme(t) {
    if (t === 'light' || t === 'dark') document.documentElement.setAttribute('data-theme', t); else document.documentElement.removeAttribute('data-theme');
  }
  applyTheme(theme);

  LU.state = Vue.reactive({
    theme: theme,
    server: saved, servers: [], editable: false, serversLoaded: false,
    plugins: [], pluginsLoaded: false, update: null, uiUpdate: null, busy: false, msg: { kind: '', text: '' }, page: (location.hash || '#overview').slice(1).split('/')[0] || 'overview'
  });

  LU.api = function (path, body) {
    var opt = { headers: { 'X-Token': token, 'X-Server': LU.state.server } };
    if (body) { opt.method = 'POST'; opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
    return fetch(path, opt).then(function (r) { return r.json(); }).catch(function (e) {
      return { ok: false, error: { code: 'no_connection', message: 'No connection to lite-ui: ' + e } };
    });
  };

  LU.say = function (kind, text) { LU.state.msg.kind = kind; LU.state.msg.text = text; };
  LU.clear = function () { LU.state.msg.kind = ''; LU.state.msg.text = ''; };
  LU.errText = function (r) { return r.error.message + (r.error.hint ? '\n' + r.error.hint : ''); };
  LU.short = function (s) { return s && s.length > 12 ? s.slice(0, 12) : (s || ''); };

  // run does one action on the server: shows a wait message, then the result; it returns the answer.
  LU.run = function (wait, path, body, okText) {
    if (LU.state.busy) return Promise.resolve({ ok: false, error: { message: 'Another action is running.' } });
    LU.state.busy = true; LU.say('ok', wait);
    return LU.api(path, body).then(function (r) {
      if (r.ok) LU.say('ok', okText ? okText(r.data) : 'Done.'); else LU.say('err', LU.errText(r));
      LU.state.busy = false;
      return r;
    });
  };

  LU.checkUpdate = function () {
    return LU.api('/api/update').then(function (r) { LU.state.update = r.ok ? r.data : null; });
  };

  // The page itself (lite-ui on this workstation) can be newer too: checkUiUpdate asks, applyUiUpdate replaces the
  // program; it restarts with the same address, so the page waits until it answers with the new version and reloads.
  LU.checkUiUpdate = function () {
    return LU.api('/api/ui-update').then(function (r) { LU.state.uiUpdate = r.ok && r.data.available ? r.data : null; });
  };
  LU.applyUiUpdate = function () {
    var u = LU.state.uiUpdate;
    if (!u || LU.state.busy) return;
    if (!confirm('Update Lite Deploy on this workstation from ' + u.current + ' to ' + u.latest + '? The page reloads in a few seconds.')) return;
    var tries = 0;
    LU.state.busy = true; LU.say('ok', 'Updating…');
    LU.api('/api/ui-update/apply', {}).then(function (r) {
      if (!r.ok) { LU.state.busy = false; LU.say('err', LU.errText(r)); return; }
      (function poll() {
        tries++;
        setTimeout(function () {
          LU.api('/api/ui-update').then(function (c) {
            if (c.ok && !c.data.available) { location.reload(); return; }
            if (tries > 30) { LU.state.busy = false; LU.say('err', 'Updated, but the page did not come back. Start lite-ui again.'); return; }
            poll();
          });
        }, 1500);
      })();
    });
  };

  // applyUpdate installs the new version on the server. The server restarts its agent, so the page waits until the
  // version reported by the server changes.
  LU.applyUpdate = function () {
    var u = LU.state.update;
    if (!u || LU.state.busy) return;
    if (!confirm('Update lite on the server from ' + u.current + ' to ' + u.latest + '? The page is unavailable for about a minute.')) return;
    var from = u.current, tries = 0;
    LU.state.busy = true; LU.say('ok', 'Updating…');
    LU.api('/api/update/apply', {}).then(function (r) {
      if (!r.ok) { LU.state.busy = false; LU.say('err', LU.errText(r)); return; }
      (function poll() {
        tries++;
        LU.api('/api/update').then(function (c) {
          if (c.ok && c.data.current !== from) { LU.state.busy = false; LU.state.update = c.data; LU.say('ok', 'Updated to ' + c.data.current + '.'); LU.loadPlugins(); return; }
          if (tries > 60) { LU.state.busy = false; LU.say('err', 'The update takes too long. Check the server.'); return; }
          setTimeout(poll, 3000);
        });
      })();
    });
  };

  // explain turns the findings of the server check into plain sentences with a button where there is something to do.
  // Items with info: true need no action and are shown as a note.
  LU.explain = function (findings) {
    var admins = null;
    findings.forEach(function (f) {
      var m = f.plugin === 'security' && f.check === 'admins' && /administrators \(sudo\): (.*?)(?:; without an SSH key: (.*))?$/.exec(f.message);
      if (m) admins = { all: m[1].split(',').map(function (x) { return x.trim(); }), nokey: (m[2] || '').split(',').map(function (x) { return x.trim(); }).filter(Boolean) };
    });
    var out = [];
    findings.forEach(function (f) {
      if (f.level === 'ok' || f.plugin === 'lite') return;
      var it = { key: f.plugin + '/' + f.check, level: f.level, text: f.message, info: false };
      var id = f.plugin + '/' + f.check;
      if (id === 'security/ssh-allow') {
        it.text = 'Anyone with an account on this server can try to log in over SSH.';
        if (admins) {
          var names = admins.all.filter(function (n) { return admins.nokey.indexOf(n) < 0; });
          if (names.length) it.action = { label: 'Allow only ' + names.join(', '), plugin: 'security', command: 'allow-users', args: names, confirm: 'Allow SSH only for ' + names.join(', ') + '? Everyone else will no longer be able to log in over SSH.' };
        }
      } else if (id === 'security/admins') {
        it.info = true;
        it.text = admins && admins.nokey.length ? 'The account ' + admins.nokey.join(', ') + ' has administrator rights but no SSH key, so nobody can log in with it. You can ignore this.' : f.message;
      } else if (id === 'security/pending-updates') {
        it.info = true;
        var n = /(\d+)/.exec(f.message);
        it.text = (n ? n[1] + ' security update(s) are' : 'Security updates are') + ' waiting. They are installed automatically.';
      } else if (id === 'security/web-auth') {
        it.text = f.level === 'ok' ? 'Guessing of the password of a site is guarded.' : 'A site has a password for direct mode, but guessing it is not guarded yet.';
        if (f.level === 'ok') it.info = true;
        else it.action = { label: 'Guard it', plugin: 'security', command: 'web-auth', args: [], confirm: 'Ban addresses that keep guessing the password of a site served directly?' };
      } else if (f.plugin === 'security' && /^(ssh-|fail2ban|auto-updates|sysctl)/.test(f.check)) {
        it.text = 'The server is not fully protected yet.';
        it.action = { label: 'Turn on protection', plugin: 'security', command: 'enable', args: [], confirm: 'Turn on protection? Password login over SSH will be closed: make sure you can log in with your key.' };
      } else if (f.plugin === 'podman' && f.check === 'version') {
        it.info = true;
        it.text = 'The container tool (podman) is an older version. Everything needed works; some newer features are unavailable.';
      } else if (f.plugin === 'backup') {
        it.text = 'Backups are not set up yet. Without them a lost server means lost data.';
        it.page = 'backups'; it.pageLabel = 'Set up backups';
      }
      out.push(it);
    });
    return out;
  };

  LU.setTheme = function (t) {
    LU.state.theme = t; applyTheme(t);
    try { localStorage.setItem('lite-ui-theme', t); } catch (e) {}
  };
  // toggles between light and dark (starting from what the system shows when the choice is automatic)
  LU.toggleTheme = function () {
    var dark = LU.state.theme === 'dark' || (LU.state.theme === 'auto' && window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches);
    LU.setTheme(dark ? 'light' : 'dark');
  };

  LU.setServer = function (v) {
    LU.state.server = v;
    try { localStorage.setItem('lite-ui-server', v); } catch (e) {}
  };

  LU.loadServers = function () {
    return LU.api('/api/servers').then(function (r) {
      var list = r.servers || [];
      LU.state.servers = list; LU.state.editable = !!r.editable; LU.state.serversLoaded = true;
      if (list.length && list.indexOf(LU.state.server) < 0) LU.setServer(list[0]);
      if (!list.length) LU.setServer('');
    });
  };

  LU.loadPlugins = function () {
    if (!LU.state.server && LU.state.editable) { LU.state.plugins = []; LU.state.pluginsLoaded = true; return Promise.resolve(); }
    return LU.api('/api/plugins').then(function (r) {
      LU.state.plugins = r.ok ? (r.data.plugins || []) : [];
      LU.state.pluginsLoaded = true;
    });
  };

  // a plugin is present unless the server says it is not installed
  LU.present = function (name) {
    var p = LU.state.plugins.filter(function (x) { return x.name === name; })[0];
    return !!p && p.installed !== false;
  };
})();
