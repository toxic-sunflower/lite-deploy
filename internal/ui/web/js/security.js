(function () {
  LU.components.Security = {
    data: function () {
      return { verify: null, fw: null, fwErr: '', port: '', proto: 'tcp', from: '', error: '', own: null, bans: null, banIp: '' };
    },
    template: '\
<div>\
  <h1>Protection</h1>\
  <p class="sub">The checks of this server, and which ports are open to the internet.</p>\
  <h3>Server check <tip text="A quick look at the server: is login by key only, is the firewall on, are security updates on. Green means fine; the line under a problem says what to do."></tip></h3>\
  <div class="row"><button class="primary" @click="check" :disabled="$root.state.busy">Check now</button>\
    <button v-if="needsFix" @click="fix" :disabled="$root.state.busy">Fix what can be fixed automatically</button></div>\
  <p v-if="error" class="down">{{ error }}</p>\
  <table v-if="verify">\
    <tr v-for="f in verify.findings" :key="f.plugin + f.check">\
      <td :class="cls(f)" style="white-space:nowrap"><icon :name="f.level === \'ok\' ? \'check\' : \'alert\'"></icon></td>\
      <td>{{ f.message }}<div class="help" v-if="f.fix && f.level !== \'ok\'">{{ f.fix }}</div></td></tr>\
  </table>\
  <div class="notice" v-if="verify"><icon name="info"></icon><div class="t">{{ nextText }}</div></div>\
  <h3>Confirm with my key <tip text="An extra lock for dangerous changes (ports, SSH, backups, publishing). When it is on, they work only if they are signed by a key that stays on this workstation, so even someone who breaks into the server cannot change them. Deploying and rolling back are not affected. It is optional: turn it on after everything is set up."></tip></h3>\
  <div class="card" v-if="own">\
    <div v-if="!own.supported" class="muted">This server is older and cannot do this yet. Update Lite Deploy on the server first.</div>\
    <template v-else>\
      <div v-if="own.required && own.registered"><b>On.</b> Changes to the server (ports, SSH, backups, publishing…) only work from this workstation, which holds your key. Deploying and rolling back are not affected.</div>\
      <div v-else-if="own.required" class="warntxt">This server asks for a key, but this workstation does not have it. Changes work only from the workstation that was paired.</div>\
      <div v-else><b>Off.</b> Anyone who gets an administrator account on the server could change it. Turn this on and the changes will only work from this workstation.</div>\
      <div class="row" style="margin-top:10px">\
        <button v-if="!own.required" class="primary" @click="pair" :disabled="$root.state.busy">Turn on</button>\
        <button v-if="own.required && own.registered" @click="unpair" :disabled="$root.state.busy">Turn off</button></div>\
      <div class="help" style="margin-top:8px">Commands typed in a terminal on the server for such changes are refused while this is on. If this workstation is lost, a person with root on the server removes <code>/etc/lite-deploy/owner-keys</code>.</div>\
    </template>\
  </div>\
  <h3>Banned addresses <tip text="The server blocks an address for an hour after many wrong passwords. If you locked yourself out, release the address here."></tip></h3>\
  <div class="card">\
    <div class="help">The server bans an address for an hour after repeated wrong SSH logins or wrong site passwords. If you locked yourself out, release the ban here.</div>\
    <div class="row" style="margin-top:8px"><button @click="showBans" :disabled="$root.state.busy">Show who is banned</button>\
      <button @click="release(\'all\')" :disabled="$root.state.busy">Release everyone</button></div>\
    <pre v-if="bans" class="out">{{ bans }}</pre>\
    <form class="row" style="margin-top:8px" @submit.prevent="release(banIp)">\
      <input class="grow" type="text" v-model="banIp" placeholder="address to release, e.g. 203.0.113.9" autocomplete="off" spellcheck="false">\
      <button type="submit" :disabled="$root.state.busy">Release this address</button></form>\
  </div>\
  <h3>Open ports <tip text="A port is a door of the server. Only the doors listed here are open to the internet. The door for logging in (SSH) and the web doors are always kept safe."></tip></h3>\
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
    computed: {
      nextText: function () {
        if (this.verify.failures) return 'Next: fix the errors above. “Fix what can be fixed automatically” handles the safe ones.';
        if (this.verify.warnings) return 'Next: the warnings are not urgent; each one says what to do.';
        return 'Next: nothing to fix. Create a project in Projects.';
      },
      needsFix: function () { return this.verify && this.verify.findings.some(function (f) { return f.fix && f.level !== 'ok' && f.plugin !== 'lite'; }); } },
    methods: {
      showBans: function () {
        var self = this;
        LU.run('Asking the server…', '/api/plugin', { plugin: 'security', command: 'banned', args: [] }, function () { return 'Done.'; })
          .then(function (r) { if (r.ok) self.bans = (r.data && r.data.output) || (r.data && r.data.text) || 'Nobody is banned.'; });
      },
      release: function (who) {
        var self = this; who = (who || '').trim();
        if (!who) { LU.say('err', 'Type an address first.'); return; }
        if (who !== 'all' && !/^[0-9a-fA-F.:]{3,45}$/.test(who)) { LU.say('err', 'That does not look like an address.'); return; }
        if (!confirm(who === 'all' ? 'Release every banned address?' : 'Release ' + who + '?')) return;
        LU.run('Releasing…', '/api/plugin', { plugin: 'security', command: 'unban', args: [who] }, function () { return who === 'all' ? 'Everyone is released.' : who + ' is released.'; })
          .then(function (r) { if (r.ok) { self.banIp = ''; self.bans = null; } });
      },
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
      loadOwner: function () {
        var self = this;
        LU.api('/api/owner').then(function (r) { self.own = r.ok ? r.data : null; });
      },
      pair: function () {
        var self = this;
        if (!confirm('Turn this on? From now on changes to the server only work from this workstation. Terminal commands on the server for such changes will be refused.')) return;
        LU.run('Registering your key…', '/api/owner/pair', { label: 'my workstation' }, function () { return 'Done. Changes to the server now need this workstation.'; }).then(function () { self.loadOwner(); });
      },
      unpair: function () {
        var self = this;
        if (!confirm('Turn this off? Changes to the server will no longer need your key.')) return;
        LU.run('Removing…', '/api/owner/unpair', {}, function () { return 'Done. Changes no longer need your key.'; }).then(function () { self.loadOwner(); });
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
    mounted: function () { this.check(); this.loadFw(); this.loadOwner(); }
  };
})();
