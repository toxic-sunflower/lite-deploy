(function () {
  // Ingress is how traffic reaches this server: the web server (Caddy), the web ports, and every domain with the way
  // visitors reach it.
  LU.components.Ingress = {
    data: function () { return { caNew: '', info: null, error: '', pick: {}, cf: null, token: '', tname: '', open: '', fa: { user: '', password: '', paths: '' }, cfOpen: '', pickT: {}, labels: [], ca: { label: 'default', token: '', remember: false, who: '', path: '', account: '' }, caApps: null, caAccounts: null }; },
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
          <option value="tunnel">Through a Cloudflare Tunnel</option><option value="direct">Directly (certificate from Caddy)</option></select>\
          <select v-if="mode(s) === \'tunnel\' && tunnelNames.length > 1" :value="tun(s)" @change="pickT[s.project + s.site] = $event.target.value" aria-label="Tunnel" style="margin-top:6px">\
            <option v-for="n in tunnelNames" :key="n" :value="n">Tunnel: {{ n }}</option></select></td>\
        <td><button v-if="changed(s)" @click="apply(s)" :disabled="$root.state.busy">Apply</button></td></tr>\
      <tr><td colspan="4" class="help">\
        <span v-if="s.protected">When served directly, {{ where(s) }} asks for a password (logins: {{ s.protected.users.join(", ") }}).</span>\
        <span v-else>When served directly, this site has no password.</span>\
        <button class="link" @click="toggle(s)">{{ open === s.project + s.site ? "Hide" : "Set a password…" }}</button> ·\
        <button class="link" @click="toggleCf(s)">{{ cfOpen === s.project + s.site ? "Hide" : "Close with Cloudflare Access…" }}</button>\
        <div v-if="cfOpen === s.project + s.site" class="box">\
          <div class="help">Cloudflare Access puts a Cloudflare login page in front of {{ s.host }} (it works while the site is reached through Cloudflare). It is set up with a Cloudflare API token that stays on THIS workstation and is never sent to the server. Create one in Cloudflare (My Profile, API Tokens) with only the permission “Access: Apps and Policies, Edit”.</div>\
          <div class="row"><div class="grow"><label>Cloudflare account</label>\
            <select v-model="ca.label"><option v-for="l in labelChoices" :key="l" :value="l">{{ l }}{{ labels.indexOf(l) >= 0 ? " (token saved)" : "" }}</option></select></div>\
            <div class="grow"><label>Or a new name</label><input type="text" v-model="caNew" placeholder="e.g. acme" autocomplete="off"></div></div>\
          <div v-if="!hasSaved"><label>API token</label><input type="password" v-model="ca.token" autocomplete="off" spellcheck="false">\
            <label class="check"><input type="checkbox" v-model="ca.remember"> Remember it on this workstation (otherwise it is forgotten after use)</label></div>\
          <div v-if="caAccounts"><label>Which Cloudflare account?</label><select v-model="ca.account"><option v-for="a in caAccounts" :key="a.id" :value="a.id">{{ a.name }}</option></select></div>\
          <label>Who may enter: e-mail addresses, or @example.com for a whole mail domain (separated by spaces)</label><input type="text" v-model="ca.who" placeholder="me@example.com" autocomplete="off">\
          <label>Path to close (empty: the whole domain)</label><input type="text" v-model="ca.path" placeholder="/admin*" autocomplete="off" spellcheck="false">\
          <div class="row" style="margin-top:10px"><button class="primary" @click="caProtect(s)" :disabled="$root.state.busy">Close it</button>\
            <button @click="caList(s)" :disabled="$root.state.busy">Show what is closed</button>\
            <button v-if="hasSaved" class="link" @click="caForget">Forget the saved token</button></div>\
          <table v-if="caApps" style="margin-top:8px"><tr v-for="a in caAppsFor(s)" :key="a.id"><td>{{ a.domain }}</td><td style="text-align:right"><button @click="caRemove(s, a)" :disabled="$root.state.busy">Open it again</button></td></tr></table>\
          <div v-if="caApps && !caAppsFor(s).length" class="help">Nothing of {{ s.host }} is closed with Cloudflare Access.</div>\
        </div>\
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
      labelChoices: function () { var l = this.labels.slice(); if (l.indexOf('default') < 0) l.unshift('default'); return l; },
      hasSaved: function () { return !this.caNew.trim() && this.labels.indexOf(this.ca.label) >= 0; },
      tunnelNames: function () { return this.cf && this.cf.tunnels ? this.cf.tunnels.map(function (x) { return x.name; }) : []; },
      needsPorts: function () { return this.info && this.info.web_ports_open === false && this.info.sites.some(function (s) { return s.publish === 'direct'; }); }
    },
    methods: {
      caReq: function (extra) {
        var label = (this.caNew.trim().toLowerCase() || this.ca.label);
        var body = { label: label, token: this.hasSaved ? '' : this.ca.token.trim(), remember: this.ca.remember, account: this.ca.account };
        for (var k in extra) body[k] = extra[k];
        return body;
      },
      caAppsFor: function (s) { return (this.caApps || []).filter(function (a) { return a.domain === s.host || a.domain.indexOf(s.host + '/') === 0; }); },
      caCall: function (wait, body, ok) {
        var self = this;
        return LU.run(wait, '/api/cf-access', this.caReq(body), ok).then(function (r) {
          if (r.ok && r.data.need_account) { self.caAccounts = r.data.accounts; self.ca.account = r.data.accounts[0] ? r.data.accounts[0].id : ''; LU.say('ok', 'This token sees several Cloudflare accounts: choose one above and press the button again.'); }
          else if (r.ok) { self.caAccounts = null; self.loadLabels(); }
          return r;
        });
      },
      caProtect: function (s) {
        var self = this, who = this.ca.who.split(/[\s,]+/).filter(Boolean);
        if (!who.length) { LU.say('err', 'Give at least one e-mail address.'); return; }
        if (!confirm('Close ' + s.host + (this.ca.path || '') + ' with Cloudflare Access? Only the listed people will get in.')) return;
        this.caCall('Closing…', { action: 'protect', host: s.host, path: this.ca.path.trim(), who: who }, function () { return s.host + (self.ca.path || '') + ' is now closed with Cloudflare Access.'; })
          .then(function (r) { if (r.ok && !r.data.need_account) { self.ca.token = ''; self.caList(s); } });
      },
      caList: function (s) {
        var self = this;
        this.caCall('Asking Cloudflare…', { action: 'list' }, function () { return 'Done.'; }).then(function (r) { if (r.ok && r.data.apps) self.caApps = r.data.apps; });
      },
      caRemove: function (s, a) {
        var self = this;
        if (!confirm('Open ' + a.domain + ' again? It will no longer ask for a Cloudflare login.')) return;
        this.caCall('Removing…', { action: 'remove', app_id: a.id }, function () { return a.domain + ' is open again.'; }).then(function (r) { if (r.ok) self.caList(s); });
      },
      caForget: function () {
        var self = this;
        LU.api('/api/cf-access', { action: 'forget', label: this.ca.label }).then(function () { self.loadLabels(); LU.say('ok', 'The saved token is forgotten.'); });
      },
      loadLabels: function () {
        var self = this;
        LU.api('/api/cf-access', { action: 'labels' }).then(function (r) { if (r.ok) { self.labels = r.data.labels; if (self.labels.length && self.labels.indexOf(self.ca.label) < 0) self.ca.label = self.labels[0]; } });
      },
      toggleCf: function (s) {
        var k = s.project + s.site;
        this.cfOpen = this.cfOpen === k ? '' : k;
        this.caApps = null; this.caAccounts = null; this.caNew = '';
        var tn = s.tunnel || 'default';
        if (this.labels.indexOf(tn) >= 0 || tn === 'default') this.ca.label = tn; else { this.ca.label = 'default'; this.caNew = tn; }
      },
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
      tun: function (s) { return this.pickT[s.project + s.site] || s.tunnel || 'default'; },
      changed: function (s) { return this.mode(s) !== s.publish || (this.mode(s) === 'tunnel' && this.tun(s) !== (s.tunnel || 'default')); },
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
        LU.run('Switching…', '/api/publish', { project: s.project, site: s.site, mode: m, tunnel: m === 'tunnel' && this.tun(s) !== 'default' ? this.tun(s) : (m === 'tunnel' && s.tunnel && s.tunnel !== 'default' ? 'default' : '') }, function () { return s.host + ' is now ' + (m === 'direct' ? 'served directly' : 'served through the tunnel') + '.'; })
          .then(function () { self.pick = {}; self.pickT = {}; self.load(); });
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
    mounted: function () { this.load(); this.loadLabels(); }
  };
})();
