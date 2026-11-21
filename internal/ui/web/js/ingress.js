(function () {
  // Ingress is how traffic reaches this server: the web server (Caddy), the web ports, and every domain with the way
  // visitors reach it.
  LU.components.Ingress = {
    data: function () { return { cfa: { token: '', name: '', account: '', accounts: '' }, acc: { who: '', path: '' }, apps: null, info: null, error: '', pick: {}, cf: null, token: '', tname: '', open: '', fa: { user: '', password: '', paths: '' }, cfOpen: '', connOpen: '', pickT: {} }; },
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
    <div class="card">\
      <div><b>Cloudflare</b> <span class="muted">{{ cfText }}</span></div>\
      <div class="help">Connect a domain through Cloudflare with one button: the server creates the tunnel, the route and the DNS record in the Cloudflare account that owns the domain. Press the button under a domain below and paste a Cloudflare API token there (the owner of the account creates it in Cloudflare: My Profile, API Tokens, Create Custom Token, with <b>Account: Cloudflare Tunnel: Edit</b>, <b>Zone: DNS: Edit</b>, <b>Zone: Zone: Read</b>). Each domain can use its own token, even from a different Cloudflare account. The token is used once on the server and is not saved anywhere.</div>\
      <table v-if="cf && cf.tunnels.length" style="margin-top:8px">\
        <tr v-for="x in cf.tunnels" :key="x.name"><td>Tunnel {{ x.name }}</td><td :class="x.running ? \'up\' : \'down\'">{{ x.running ? "connected and running" : "connected but not running" }}</td>\
          <td style="text-align:right"><button @click="cfRemove(x.name)" :disabled="$root.state.busy">Disconnect</button></td></tr>\
      </table>\
      <details style="margin-top:8px"><summary class="help">I already have a tunnel token (the owner made the tunnel by hand)</summary>\
        <form class="row" style="margin-top:8px" @submit.prevent="cfConnect">\
          <input style="width:140px" type="text" v-model="tname" placeholder="Name, e.g. acme" autocomplete="off" spellcheck="false">\
          <input class="grow" type="password" v-model="token" placeholder="Tunnel token" autocomplete="off" spellcheck="false">\
          <button type="submit" :disabled="$root.state.busy">Connect the tunnel</button></form></details>\
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
        <button class="primary" @click="toggleConnect(s)">{{ connOpen === s.project + s.site ? "Hide" : "Connect through Cloudflare" }}</button> ·\
        <button class="link" @click="toggleCf(s)">{{ cfOpen === s.project + s.site ? "Hide" : "Close with a Cloudflare login…" }}</button>\
        <div v-if="connOpen === s.project + s.site" class="box">\
          <div class="row"><div class="grow"><label>Cloudflare API token for {{ s.host }}</label><input type="password" v-model="cfa.token" autocomplete="off" spellcheck="false"></div>\
            <div class="grow"><label>Tunnel name (any; domains with the same name share one tunnel)</label><input type="text" v-model="cfa.name" placeholder="default" autocomplete="off" spellcheck="false"></div></div>\
          <div v-if="cfa.accounts" class="help warntxt">This token sees several Cloudflare accounts: {{ cfa.accounts }}. Put the id of the right one here and press the button again: <input type="text" v-model="cfa.account" style="width:260px" autocomplete="off"></div>\
          <div class="row" style="margin-top:10px"><button class="primary" @click="cfaConnect(s)" :disabled="$root.state.busy">Connect {{ s.host }}</button></div>\
        </div>\
        <div v-if="cfOpen === s.project + s.site" class="box">\
          <div class="help">A Cloudflare login page in front of {{ s.host }}, with the token above (Access also needs the permission <b>Account: Access: Apps and Policies: Edit</b>).</div>\
          <label>Cloudflare API token</label><input type="password" v-model="cfa.token" autocomplete="off" spellcheck="false">\
          <label>Who may enter (e-mail addresses, or @example.com for a whole mail domain, separated by spaces)</label><input type="text" v-model="acc.who" placeholder="me@example.com" autocomplete="off">\
          <label>Path to close (empty: the whole domain)</label><input type="text" v-model="acc.path" placeholder="/admin*" autocomplete="off" spellcheck="false">\
          <div class="row" style="margin-top:10px"><button class="primary" @click="cfaProtect(s)" :disabled="$root.state.busy">Close it</button>\
            <button @click="cfaList(s)" :disabled="$root.state.busy">Show what is closed</button></div>\
          <table v-if="apps" style="margin-top:8px"><tr v-for="a in appsFor(s)" :key="a.id"><td>{{ a.domain }}</td><td style="text-align:right"><button @click="cfaRemove(s, a)" :disabled="$root.state.busy">Open it again</button></td></tr></table>\
          <div v-if="apps && !appsFor(s).length" class="help">Nothing of {{ s.host }} is closed.</div>\
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
        if (!this.cfPresent) return 'is installed on the server when you connect the first domain';
        if (!this.cf) return '…';
        if (!this.cf.tunnels.length) return 'is installed; paste the tunnel token to connect it';
        return this.cf.tunnels.length + (this.cf.tunnels.length === 1 ? ' tunnel' : ' tunnels');
      },
      tunnelNames: function () { return this.cf && this.cf.tunnels ? this.cf.tunnels.map(function (x) { return x.name; }) : []; },
      needsPorts: function () { return this.info && this.info.web_ports_open === false && this.info.sites.some(function (s) { return s.publish === 'direct'; }); }
    },
    methods: {
      cfaName: function () { return (this.cfa.name || 'default').trim().toLowerCase(); },
      cfaCall: function (wait, args, ok) {
        var self = this, tok = this.cfa.token.trim();
        if (!tok) { LU.say('err', 'Paste the Cloudflare API token first.'); return Promise.resolve({ ok: false }); }
        if (this.cfa.account) args = args.concat(['--account', this.cfa.account.trim()]);
        return LU.run(wait, '/api/plugin', { plugin: 'cloudflare', command: args[0], args: args.slice(1).concat(['--token-stdin']), stdin: tok + '\n' }, ok).then(function (r) {
          if (!r.ok && r.error && /several Cloudflare accounts/.test(r.error.message || '')) { self.cfa.accounts = r.error.hint || ''; }
          return r;
        });
      },
      cfaConnect: function (s) {
        var self = this, name = this.cfaName();
        if (!/^[a-z][a-z0-9-]{0,20}$/.test(name)) { LU.say('err', 'The tunnel name is lowercase letters, digits and dashes, starting with a letter.'); return; }
        if (!s.port) { LU.say('err', 'The project is not created on the server yet, so the site has no port. Create the project first.'); return; }
        if (!confirm('Connect ' + s.host + ' through Cloudflare? The server creates the tunnel and the DNS record in the Cloudflare account and points ' + s.project + '/' + s.site + ' at it. A DNS record of this name is replaced.')) return;
        var args = ['connect', '--domain', s.host, '--port', String(s.port), '--project', s.project, '--site', s.site];
        if (name !== 'default') args = args.concat(['--name', name]);
        this.cfaCall('Setting up Cloudflare…', args, function () { return s.host + ' is connected through Cloudflare.'; })
          .then(function (r) { if (r.ok) { self.cfa.token = ''; self.cfa.accounts = ''; self.loadCf(); self.load(); } });
      },
      cfaProtect: function (s) {
        var self = this, who = this.acc.who.split(/[\s,]+/).filter(Boolean);
        if (!who.length) { LU.say('err', 'Give at least one e-mail address.'); return; }
        if (!confirm('Close ' + s.host + (this.acc.path || '') + ' with a Cloudflare login? Only the listed people will get in.')) return;
        var args = ['access-protect', '--domain', s.host, '--who', who.join(',')];
        if (this.acc.path.trim()) args = args.concat(['--path', this.acc.path.trim()]);
        this.cfaCall('Closing…', args, function () { return s.host + ' is closed with a Cloudflare login.'; });
      },
      cfaList: function (s) {
        var self = this;
        this.cfaCall('Asking Cloudflare…', ['access-list'], function () { return 'Done.'; }).then(function (r) { if (r.ok && r.data && r.data.apps) self.apps = r.data.apps; });
      },
      cfaRemove: function (s, a) {
        var self = this;
        if (!confirm('Open ' + a.domain + ' again?')) return;
        this.cfaCall('Removing…', ['access-remove', '--app', a.id], function () { return a.domain + ' is open again.'; }).then(function (r) { if (r.ok) self.cfaList(s); });
      },
      appsFor: function (s) { return (this.apps || []).filter(function (a) { return a.domain === s.host || a.domain.indexOf(s.host + '/') === 0; }); },
      toggleConnect: function (s) { var k = s.project + s.site; this.connOpen = this.connOpen === k ? '' : k; this.cfa.accounts = ''; this.cfa.account = ''; },
      toggleCf: function (s) { var k = s.project + s.site; this.cfOpen = this.cfOpen === k ? '' : k; this.apps = null; },
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
    mounted: function () { this.load(); }
  };
})();
