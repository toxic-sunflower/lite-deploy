(function () {
  // Ingress is how traffic reaches this server: the web server (Caddy), the web ports, and every domain with the way
  // visitors reach it.
  LU.components.Ingress = {
    data: function () { return { info: null, error: '', pick: {} }; },
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
    <h3>Domains</h3>\
    <p v-if="!info.sites.length" class="empty">No domains yet. A domain is added when you create a project.</p>\
    <table v-else>\
      <tr><th>Domain</th><th>Application</th><th>How visitors reach it</th><th></th></tr>\
      <tr v-for="s in info.sites" :key="s.project + s.site">\
        <td>{{ s.host }}</td><td>{{ s.project }} / {{ s.service }}</td>\
        <td><select :value="mode(s)" @change="pick[s.project + s.site] = $event.target.value">\
          <option value="tunnel">Through a Cloudflare Tunnel</option><option value="direct">Directly (certificate from Caddy)</option></select></td>\
        <td><button v-if="mode(s) !== s.publish" @click="apply(s)" :disabled="$root.state.busy">Apply</button></td></tr>\
    </table>\
    <div class="help">Through a Cloudflare Tunnel: nothing on this server is open to the internet. Directly: the server answers on ports 80 and 443 and gets the certificate itself. If Cloudflare has a problem, switch a domain to “Directly” and point its DNS record at this server.</div>\
  </template>\
  <p v-else-if="!error" class="empty">Loading…</p>\
</div>',
    computed: {
      needsPorts: function () { return this.info && this.info.web_ports_open === false && this.info.sites.some(function (s) { return s.publish === 'direct'; }); }
    },
    methods: {
      mode: function (s) { return this.pick[s.project + s.site] || s.publish; },
      load: function () {
        var self = this; self.error = '';
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
