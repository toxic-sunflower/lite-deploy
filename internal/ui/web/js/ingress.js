(function () {
  // Ingress is how traffic reaches this server: the web server (Caddy), the web ports, and every domain with the way
  // visitors reach it.
  LU.components.Ingress = {
    data: function () { return { info: null, error: '', pick: {}, cf: null, token: '', tname: '', open: '', fa: { user: '', password: '', paths: '' } }; },
    template: '\
<div>\
  <h1>Ingress</h1>\
  <p class="sub">How visitors reach this server, and which domain leads to which application.</p>\
  <p v-if="error" class="down">{{ error }}</p>\
  <template v-if="info">\
    <div class="card row spread">\
      <div><b>Web server (Caddy)</b> <span class="muted">{{ info.caddy.installed ? (info.caddy.running ? "is running" : "is installed but not running") : "is not installed" }}</span></div>\
      <button v-if="!info.caddy.installed" class="primary" @click="installCaddy" :disabled="$root.state.busy">Install</button>\
    </div>\
    <div v-if="needsPorts" class="notice warn"><div class="t">Some domains are reached directly, but the web ports 80 and 443 are closed, so visitors cannot get in and no certificate can be issued.</div>\
      <button class="primary" @click="openPorts" :disabled="$root.state.busy">Open the web ports</button></div>\
    <div v-if="cfPresent" class="card">\
      <div><b>Cloudflare Tunnels</b> <span class="muted">{{ cfText }}</span></div>\
      <table v-if="cf && cf.tunnels.length" style="margin-top:6px">\
        <tr v-for="x in cf.tunnels" :key="x.name"><td>{{ x.name }}</td><td :class="x.running ? \'up\' : \'down\'">{{ x.running ? "connected and running" : "connected but not running" }}</td>\
          <td style="text-align:right"><button @click="cfRemove(x.name)" :disabled="$root.state.busy">Disconnect</button></td></tr>\
      </table>\
      <form v-if="cf" class="row" style="margin-top:8px" @submit.prevent="cfConnect">\
        <input v-if="cf.tunnels.length" style="width:140px" type="text" v-model="tname" placeholder="Name, e.g. acme" autocomplete="off" spellcheck="false">\
        <input class="grow" type="password" v-model="token" :placeholder="cf.tunnels.length ? \'Token of another Cloudflare account or tunnel\' : \'Tunnel token from the Cloudflare dashboard\'" autocomplete="off" spellcheck="false">\
        <button class="primary" type="submit" :disabled="$root.state.busy">{{ cf.tunnels.length ? "Add tunnel" : "Connect" }}</button></form>\
      <div class="help">Each Cloudflare account has its own tunnel and its own token. In that account’s dashboard route the domain to <code>http://127.0.0.1:PORT</code> of the site.</div>\
    </div>\
    <h3>Domains</h3>\
    <p v-if="!info.sites.length" class="empty">No domains yet. A domain is added when you create a project.</p>\
    <table v-else>\
      <template v-for="s in info.sites" :key="s.project + s.site">\
      <tr>\
        <td>{{ s.host }}</td><td>{{ s.project }} / {{ s.service }}</td>\
        <td><select :value="mode(s)" @change="pick[s.project + s.site] = $event.target.value">\
          <option value="tunnel">Through a Cloudflare Tunnel</option><option value="direct">Directly (certificate from Caddy)</option></select></td>\
        <td><button v-if="mode(s) !== s.publish" @click="apply(s)" :disabled="$root.state.busy">Apply</button></td></tr>\
      <tr><td colspan="4" class="help">\
        <span v-if="s.protected">When served directly, {{ where(s) }} asks for a password (logins: {{ s.protected.users.join(", ") }}).</span>\
        <span v-else>When served directly, this site has no password.</span>\
        <button class="link" @click="toggle(s)">{{ open === s.project + s.site ? "Close" : "Set a password…" }}</button>\
        <div v-if="open === s.project + s.site" class="box">\
          <div class="help">A fallback for when Cloudflare is not in front of the site: it is asked for only in the “Directly” mode, never in the tunnel mode. Only a hash of the password is kept on the server.</div>\
          <label>Paths to close, separated by spaces (empty: the whole site)</label><input type="text" v-model="fa.paths" placeholder="/admin*" autocomplete="off" spellcheck="false">\
          <div class="row"><div class="grow"><label>Login</label><input type="text" v-model="fa.user" placeholder="admin" autocomplete="off"></div>\
            <div class="grow"><label>Password (12 characters or more)</label><input type="password" v-model="fa.password" autocomplete="new-password"></div></div>\
          <div class="row" style="margin-top:10px"><button class="primary" @click="faSet(s)" :disabled="$root.state.busy">Save</button>\
            <button v-if="s.protected" @click="faOff(s)" :disabled="$root.state.busy">Remove the password</button></div>\
        </div></td></tr>\
      </template>\
    </table>\
    <div class="help">Through a Cloudflare Tunnel: nothing on this server is open to the internet. Directly: the server answers on ports 80 and 443 and gets the certificate itself. If Cloudflare has a problem, switch a domain to “Directly” and point its DNS record at this server.</div>\
  </template>\
  <p v-else-if="!error" class="empty">Loading…</p>\
</div>',
    watch: { cfPresent: function (v) { if (v) this.loadCf(); } },
    computed: {
      cfPresent: function () { return LU.present('cloudflare'); },
      cfText: function () {
        if (!this.cf) return '…';
        if (!this.cf.tunnels.length) return 'is installed; paste the tunnel token to connect it';
        return this.cf.tunnels.length + (this.cf.tunnels.length === 1 ? ' tunnel' : ' tunnels');
      },
      needsPorts: function () { return this.info && this.info.web_ports_open === false && this.info.sites.some(function (s) { return s.publish === 'direct'; }); }
    },
    methods: {
      where: function (s) { return s.protected.paths && s.protected.paths.length ? s.protected.paths.join(' ') : 'the whole site'; },
      toggle: function (s) {
        var k = s.project + s.site;
        this.open = this.open === k ? '' : k;
        this.fa = { user: '', password: '', paths: s.protected && s.protected.paths ? s.protected.paths.join(' ') : '' };
      },
      faSet: function (s) {
        var self = this, paths = this.fa.paths.split(/\s+/).filter(Boolean);
        if (!/^[a-z][a-z0-9._-]{0,30}$/.test(this.fa.user)) { LU.say('err', 'The login is lowercase letters, digits, dots and dashes, starting with a letter.'); return; }
        if (this.fa.password.length < 12) { LU.say('err', 'The password must be at least 12 characters.'); return; }
        if (!confirm('Set the password for ' + s.host + '? It is asked for when the site is served directly.')) return;
        LU.run('Saving…', '/api/fallback-auth', { project: s.project, site: s.site, action: 'set', user: this.fa.user, password: this.fa.password, paths: paths },
          function () { return 'Saved. The password is asked for when ' + s.host + ' is served directly.'; })
          .then(function (r) { if (r.ok) { self.open = ''; self.fa = { user: '', password: '', paths: '' }; } self.load(); });
      },
      faOff: function (s) {
        var self = this;
        if (!confirm('Remove the password of ' + s.host + '? When served directly it will be open to everyone.')) return;
        LU.run('Removing…', '/api/fallback-auth', { project: s.project, site: s.site, action: 'off' }, function () { return 'The password is removed.'; })
          .then(function () { self.open = ''; self.load(); });
      },
      mode: function (s) { return this.pick[s.project + s.site] || s.publish; },
      loadCf: function () {
        var self = this;
        if (!LU.present('cloudflare')) { self.cf = null; return; }
        LU.api('/api/plugin', { plugin: 'cloudflare', command: 'status', args: [] }).then(function (r) { self.cf = r.ok && r.data && r.data.tunnels ? r.data : null; });
      },
      cfConnect: function () {
        var self = this, tok = this.token.trim(), name = this.tname.trim().toLowerCase();
        if (!tok) { LU.say('err', 'Paste the tunnel token first.'); return; }
        var have = this.cf && this.cf.tunnels.length;
        if (have && !name) { LU.say('err', 'Give the new tunnel a name, for example the account it belongs to.'); return; }
        if (name && !/^[a-z][a-z0-9-]{0,20}$/.test(name)) { LU.say('err', 'A tunnel name is lowercase letters, digits and dashes, starting with a letter.'); return; }
        var body = { token: tok }; if (name) body.name = name;
        LU.run('Connecting the tunnel…', '/api/cloudflare/setup', body, function () { return 'The tunnel is connected. Now route your domain to the site in the Cloudflare dashboard.'; })
          .then(function (r) { if (r.ok) { self.token = ''; self.tname = ''; } self.loadCf(); });
      },
      cfRemove: function (name) {
        var self = this;
        if (!confirm('Disconnect the tunnel ' + name + '? Sites that are reached through it stop working until you connect it again.')) return;
        LU.run('Disconnecting…', '/api/plugin', { plugin: 'cloudflare', command: 'remove', args: name === 'default' ? [] : ['--name', name] }, function () { return 'The tunnel is disconnected.'; }).then(function () { self.loadCf(); });
      },
      load: function () {
        var self = this; self.error = ''; self.loadCf();
        LU.api('/api/ingress').then(function (r) { if (r.ok) self.info = r.data; else self.error = r.error ? r.error.message : 'Could not load.'; });
      },
      apply: function (s) {
        var self = this, m = this.mode(s);
        var q = m === 'direct' ? 'Serve ' + s.host + ' directly? Point its DNS record at this server and open the web ports.' : 'Serve ' + s.host + ' only through the Cloudflare Tunnel?';
        if (!confirm(q)) return;
        LU.run('Switching…', '/api/publish', { project: s.project, site: s.site, mode: m }, function () { return s.host + ' is now ' + (m === 'direct' ? 'served directly' : 'served through the tunnel') + '.'; })
          .then(function () { self.pick = {}; self.load(); });
      },
      openPorts: function () {
        var self = this;
        if (!confirm('Open ports 80 and 443 to the internet?')) return;
        LU.run('Opening…', '/api/plugin', { plugin: 'firewall', command: 'mode', args: ['http'] }, function () { return 'The web ports are open.'; }).then(function () { self.load(); });
      },
      installCaddy: function () {
        var self = this;
        if (!confirm('Install Caddy on this server?')) return;
        LU.run('Installing Caddy…', '/api/plugin', { plugin: 'caddy', command: 'install', args: [] }, function () { return 'Caddy is installed.'; }).then(function () { LU.loadPlugins(); self.load(); });
      }
    },
    mounted: function () { this.load(); }
  };
})();
