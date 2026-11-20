(function () {
  LU.components.Security = {
    data: function () {
      return { verify: null, fw: null, fwErr: '', port: '', proto: 'tcp', from: '', error: '' };
    },
    template: '\
<div>\
  <h1>Protection</h1>\
  <p class="sub">The checks of this server, and which ports are open to the internet.</p>\
  <h3>Server check</h3>\
  <div class="row"><button class="primary" @click="check" :disabled="$root.state.busy">Check now</button>\
    <button v-if="needsFix" @click="fix" :disabled="$root.state.busy">Fix what can be fixed automatically</button></div>\
  <p v-if="error" class="down">{{ error }}</p>\
  <table v-if="verify">\
    <tr v-for="f in verify.findings" :key="f.plugin + f.check">\
      <td :class="cls(f)" style="white-space:nowrap">{{ f.level === "ok" ? "ok" : (f.level === "warn" ? "warning" : "error") }}</td>\
      <td>{{ f.message }}<div class="help" v-if="f.fix && f.level !== \'ok\'">{{ f.fix }}</div></td></tr>\
  </table>\
  <h3>Open ports</h3>\
  <p v-if="fwErr" class="down">{{ fwErr }}</p>\
  <div class="card" v-if="fw">\
    <div class="muted">The firewall is {{ fw.active ? "on" : "off" }}. SSH port: {{ fw.ssh_port }}.</div>\
    <button v-if="!fw.active" class="primary" style="margin-top:8px" @click="enable" :disabled="$root.state.busy">Turn on</button>\
    <table><tr><th>Port</th><th>Action</th><th>From</th><th></th></tr>\
      <tr v-for="r in fw.rules" :key="r.raw">\
        <td>{{ r.port ? r.port + (r.proto ? "/" + r.proto : "") : r.raw }}</td><td>{{ r.action }}</td><td>{{ r.from }}</td>\
        <td><span v-if="r.protected" class="help">always open</span><button v-else-if="r.port" @click="closePort(r)" :disabled="$root.state.busy">Close</button></td></tr></table>\
    <form class="row" @submit.prevent="open">\
      <input style="width:100px" type="text" v-model="port" placeholder="port" inputmode="numeric" maxlength="5" autocomplete="off">\
      <select style="width:90px" v-model="proto"><option>tcp</option><option>udp</option></select>\
      <input class="grow" type="text" v-model="from" placeholder="only from this address (optional)" autocomplete="off" spellcheck="false">\
      <button class="primary" type="submit" :disabled="$root.state.busy">Open port</button></form>\
  </div>\
</div>',
    computed: { needsFix: function () { return this.verify && this.verify.findings.some(function (f) { return f.fix && f.level !== 'ok' && f.plugin !== 'lite'; }); } },
    methods: {
      cls: function (f) { return f.level === 'ok' ? 'up' : (f.level === 'warn' ? 'warntxt' : 'down'); },
      check: function () {
        var self = this; self.error = '';
        LU.api('/api/verify').then(function (r) {
          if (r.data) self.verify = r.data; else self.error = r.error ? r.error.message : 'The check did not run.';
        });
      },
      fix: function () {
        var self = this;
        if (!confirm('Apply the safe fixes? The firewall, users and keys are not touched.')) return;
        LU.run('Fixing…', '/api/fix', {}, function (d) { return d.applied.length ? 'Done: ' + d.applied.join('; ') : 'Nothing to fix automatically.'; }).then(function () { self.check(); });
      },
      loadFw: function () {
        var self = this; self.fwErr = '';
        LU.api('/api/firewall').then(function (r) {
          if (r.ok) self.fw = r.data; else self.fwErr = r.error ? r.error.message : 'Could not read the firewall.';
        });
      },
      enable: function () {
        var self = this;
        if (!confirm('Turn the firewall on? SSH (port ' + this.fw.ssh_port + '), 80 and 443 stay open, other incoming traffic is closed.')) return;
        LU.run('Turning on…', '/api/firewall/enable', {}, function () { return 'The firewall is on.'; }).then(function () { self.loadFw(); });
      },
      open: function () {
        var self = this, n = parseInt(this.port, 10);
        if (!(n >= 1 && n <= 65535)) { LU.say('err', 'Port: a number from 1 to 65535.'); return; }
        var from = this.from.trim();
        if (!confirm('Open ' + n + '/' + this.proto + (from ? ' for ' + from : ' for everyone') + '?')) return;
        LU.run('Opening…', '/api/firewall/allow', { port: n, proto: this.proto, from: from }, function () { return 'Port ' + n + '/' + self.proto + ' is open.'; }).then(function () { self.port = ''; self.loadFw(); });
      },
      closePort: function (r) {
        var self = this;
        if (!confirm('Close ' + r.port + '/' + r.proto + (r.from !== 'any' ? ' for ' + r.from : '') + '?')) return;
        LU.run('Closing…', '/api/firewall/remove', { port: r.port, proto: r.proto, from: r.from === 'any' ? '' : r.from, action: r.action }, function () { return 'Port ' + r.port + ' is closed.'; }).then(function () { self.loadFw(); });
      }
    },
    mounted: function () { this.check(); this.loadFw(); }
  };
})();
