(function () {
  function download(name, text) {
    var a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([text], { type: 'text/plain' }));
    a.download = name; document.body.appendChild(a); a.click(); a.remove();
  }
  LU.components.Backups = {
    data: function () { return { st: null, list: [], error: '', encrypt: true, stores: ['backblaze', 's3', 'sftp'], open: '' }; },
    template: '\
<div>\
  <h1>Backups</h1>\
  <p class="sub">Copies of your project settings, secrets and databases, encrypted with a key that only you keep.</p>\
  <p v-if="error" class="down">{{ error }}</p>\
  <template v-if="st && !st.configured">\
    <div class="card"><h2>Set up backups</h2>\
      <p>1. Press the button. A key is made on this computer and a file with the private part is saved on it. <b>Keep that file safe and off the server</b>: without it the backups cannot be opened.</p>\
      <label class="check"><input type="checkbox" v-model="encrypt"> Encrypt database copies too (recommended)</label>\
      <p class="help">2. The matching public part is sent to the server, and the server starts making encrypted backups.</p>\
      <button class="primary" @click="setup" :disabled="$root.state.busy">Create my key and turn on backups</button></div>\
  </template>\
  <template v-if="st && st.configured">\
    <div class="card row spread"><div><b>Backups are set up</b><div class="help">Daily backup: {{ st.scheduled ? "on, around 03:00" : "off" }}. {{ st.count }} settings backup(s){{ st.last ? ", the last one " + when(st.last) : "" }}.</div></div>\
      <div class="row"><button @click="toggle" :disabled="$root.state.busy">{{ st.scheduled ? "Turn daily backup off" : "Turn daily backup on" }}</button>\
        <button class="primary" @click="runNow" :disabled="$root.state.busy">Back up now</button></div></div>\
    <h3>Settings and secrets backups</h3>\
    <p v-if="!list.length" class="empty">No backups yet. Press “Back up now”.</p>\
    <table v-else><tr><th>File</th></tr><tr v-for="f in list" :key="f"><td><code>{{ f }}</code></td></tr></table>\
    <h3>Copies outside this server (optional)</h3>\
    <p class="help">A backup that lives only on the server is lost with the server. Add a place outside it.</p>\
    <div v-for="n in storesPresent" :key="n" class="card"><div class="row spread"><b>{{ n }}</b><button @click="open = open === n ? \'\' : n">{{ open === n ? "Hide" : "Set up" }}</button></div>\
      <plugin-panel v-if="open === n" :plugin="plugin(n)" @done="load"></plugin-panel></div>\
    <p v-if="!storesPresent.length" class="muted">Add Backblaze, S3 or SFTP in <a class="link" @click="$root.go(\'server\')" style="cursor:pointer">Server</a> to keep copies outside this server.</p>\
  </template>\
</div>',
    computed: { storesPresent: function () { var self = this; return this.stores.filter(function (n) { return LU.state.plugins.some(function (p) { return p.name === n; }); }); } },
    methods: {
      when: function (iso) { var d = new Date(iso); return isNaN(d) ? iso : d.toLocaleString(); },
      plugin: function (n) { return LU.state.plugins.filter(function (p) { return p.name === n; })[0]; },
      call: function (command, args) { return LU.api('/api/plugin', { plugin: 'backup', command: command, args: args }); },
      load: function () {
        var self = this; self.error = '';
        self.call('status', []).then(function (r) { if (r.ok) self.st = r.data; else self.error = r.error ? r.error.message : 'Could not read the state.'; });
        self.call('list', []).then(function (r) { self.list = r.ok ? (r.data.config || []) : []; });
      },
      setup: function () {
        var self = this;
        LU.run('Making your key…', '/api/backup/keygen', {}, function () { return ''; }).then(function (r) {
          if (!r.ok) return;
          // the page saves the private part on this computer first: only then is it sent anywhere
          download('lite-backup.key', '# lite-deploy backup key. Keep it safe and OFF the server.\n# public key: ' + r.data.public + '\n' + r.data.private + '\n');
          if (!confirm('A file “lite-backup.key” was saved to your Downloads. Keep it somewhere safe, off the server: without it backups cannot be opened.\n\nIs the file saved? Press OK to turn on backups.')) { LU.say('err', 'Nothing was turned on. Run it again when the key is saved.'); return; }
          var args = [r.data.public]; if (self.encrypt) args.push('--encrypt-dumps');
          LU.run('Turning on backups…', '/api/plugin', { plugin: 'backup', command: 'setup', args: args }, function () { return 'Backups are set up.'; }).then(function (s) {
            if (!s.ok) return;
            LU.run('Starting the daily schedule…', '/api/plugin', { plugin: 'backup', command: 'schedule', args: ['on'] }, function () { return 'Backups are on: daily around 03:00. Press “Back up now” to try one.'; }).then(function () { self.load(); });
          });
        });
      },
      toggle: function () {
        var self = this, on = !this.st.scheduled;
        LU.run(on ? 'Turning on…' : 'Turning off…', '/api/plugin', { plugin: 'backup', command: 'schedule', args: [on ? 'on' : 'off'] }, function () { return on ? 'Daily backups are on.' : 'Daily backups are off.'; }).then(function () { self.load(); });
      },
      runNow: function () {
        var self = this;
        LU.run('Taking a backup…', '/api/plugin', { plugin: 'backup', command: 'run', args: [] }, function () { return 'A backup was taken.'; }).then(function () { self.load(); });
      }
    },
    mounted: function () { this.load(); }
  };
})();
