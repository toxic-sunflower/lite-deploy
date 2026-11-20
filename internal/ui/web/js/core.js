(function () {
  var LU = window.LU = { components: {} };
  var token = new URLSearchParams(location.search).get('t') || sessionStorage.getItem('t') || '';
  if (token) { sessionStorage.setItem('t', token); history.replaceState(null, '', location.pathname + location.hash); }
  var saved = '';
  try { saved = localStorage.getItem('lite-ui-server') || ''; } catch (e) {}

  LU.state = Vue.reactive({
    server: saved, servers: [], editable: false, serversLoaded: false,
    plugins: [], pluginsLoaded: false, update: null, busy: false, msg: { kind: '', text: '' }, page: (location.hash || '#overview').slice(1).split('/')[0] || 'overview'
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
