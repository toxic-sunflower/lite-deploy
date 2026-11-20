(function () {
  function download(name, text) {
    var a = document.createElement('a');
    a.href = URL.createObjectURL(new Blob([text], { type: 'text/plain' }));
    a.download = name; document.body.appendChild(a); a.click(); a.remove();
  }
  // The places a backup can be copied to. Each form builds the arguments and the standard input of the plugin command, so a
  // key never travels as an argument.
  var STORES = {
    backblaze: { title: 'Backblaze B2', fields: [['bucket', 'Bucket name', 'text'], ['prefix', 'Folder in the bucket (optional)', 'text'], ['id', 'Key ID', 'text'], ['key', 'Application key', 'password']],
      build: function (v) { return { args: [v.bucket].concat(v.prefix ? [v.prefix] : []), stdin: v.id + '\n' + v.key + '\n' }; } },
    s3: { title: 'S3-compatible storage', fields: [['address', 'Address (https://…)', 'text'], ['bucket', 'Bucket name', 'text'], ['prefix', 'Folder in the bucket (optional)', 'text'], ['id', 'Access key', 'text'], ['key', 'Secret key', 'password']],
      build: function (v) { return { args: [v.address, v.bucket].concat(v.prefix ? [v.prefix] : []), stdin: v.id + '\n' + v.key + '\n' }; } },
    sftp: { title: 'Another server (SFTP)', fields: [['host', 'Server address', 'text'], ['user', 'User', 'text'], ['dir', 'Folder on that server', 'text'], ['pkey', 'Private SSH key of that user', 'area'], ['known', 'The server key line (ssh-keyscan -t ed25519 ADDRESS)', 'text']],
      build: function (v) { return { args: [v.host, v.user, v.dir], stdin: v.pkey.trim() + '\n---\n' + v.known.trim() + '\n' }; } }
  };

  LU.components.StoreForm = {
    props: ['store'],
    data: function () { return { v: {} }; },
    template: '\
<div class="card"><h2>{{ def.title }}</h2>\
  <form @submit.prevent="save">\
    <div v-for="f in def.fields" :key="f[0]"><label>{{ f[1] }}</label>\
      <textarea v-if="f[2] === \'area\'" v-model="v[f[0]]" spellcheck="false"></textarea>\
      <input v-else :type="f[2]" v-model="v[f[0]]" autocomplete="off" spellcheck="false"></div>\
    <div style="margin-top:12px"><button class="primary" type="submit" :disabled="$root.state.busy">Connect and copy a backup</button></div>\
  </form>\
</div>',
    computed: { def: function () { return STORES[this.store]; } },
    methods: {
      save: function () {
        var self = this, b = this.def.build(this.v), n = this.store;
        LU.run('Preparing…', '/api/plugin', { plugin: n, command: 'install', args: [] }, function () { return ''; }).then(function (r) {
          if (!r.ok) return;
          LU.run('Connecting…', '/api/plugin', { plugin: n, command: 'setup', args: b.args, stdin: b.stdin }, function () { return 'Connected.'; }).then(function (s) {
            if (!s.ok) return;
            LU.run('Copying a backup…', '/api/plugin', { plugin: n, command: 'upload', args: [] }, function () { return 'Backups are copied to ' + self.def.title + '. A daily copy is turned on next.'; }).then(function () {
              LU.run('Turning on the daily copy…', '/api/plugin', { plugin: n, command: 'schedule', args: ['on'] }, function () { return 'Done: backups are also copied to ' + self.def.title + ' every day.'; }).then(function () { self.v = {}; self.$emit('done'); });
            });
          });
        });
      }
    }
  };

  LU.components.Backups = {
    data: function () { return { st: null, list: [], error: '', encrypt: true, stores: ['backblaze', 's3', 'sftp'], open: '', edit: {} }; },
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
    <h3>What is backed up</h3>\
    <p class="help">Choose for the server itself and for each project what is copied, where, and when. The server means the web server (Caddy) and the settings of lite. A project means its settings, secrets, database and files.</p>\
    <template v-for="tg in (st.targets || [])" :key="tg.name"><div class="card" v-if="edit[tg.name]">\
      <div class="row spread"><h2><icon :name="tg.kind === \'server\' ? \'server\' : \'apps\'"></icon> {{ tg.kind === "server" ? "The server" : tg.name }}</h2>\
        <label class="check" style="margin:0"><input type="checkbox" v-model="edit[tg.name].enabled"> back it up</label></div>\
      <div class="help">{{ tg.last ? "Last backup: " + when(tg.last) : "No backup yet." }}</div>\
      <label>What</label>\
      <div class="row"><label v-for="it in itemsFor(tg)" :key="it" class="check" style="margin:0"><input type="checkbox" :value="it" v-model="edit[tg.name].items"> {{ itemName(it) }}</label></div>\
      <label>Where to copy (outside this server)</label>\
      <div class="row" v-if="st.stores.length"><label v-for="s in st.stores" :key="s" class="check" style="margin:0"><input type="checkbox" :value="s" v-model="edit[tg.name].stores"> {{ title(s) }}</label>\
        <span class="help">{{ edit[tg.name].stores.length ? "" : "none ticked: every connected place" }}</span></div>\
      <div v-else class="help">No place outside this server is connected yet (below).</div>\
      <label>When (every day, HH:MM; empty: with the common backup around 03:00)</label>\
      <input type="text" v-model="edit[tg.name].at" placeholder="02:30" style="max-width:140px">\
      <div class="row" style="margin-top:10px"><button class="primary" @click="saveTarget(tg)" :disabled="$root.state.busy">Save</button>\
        <button @click="runTarget(tg)" :disabled="$root.state.busy">Back up this now</button></div>\
    </div></template>\
    <h3>Settings and secrets backups</h3>\
    <p v-if="!list.length" class="empty">No backups yet. Press “Back up now”.</p>\
    <table v-else><tr><th>File</th></tr><tr v-for="f in list" :key="f"><td><code>{{ f }}</code></td></tr></table>\
    <h3>Copies outside this server (optional)</h3>\
    <p class="help">A backup that lives only on the server is lost with the server. Add a place outside it.</p>\
    <div v-for="n in stores" :key="n" class="notice"><icon name="backup"></icon><div class="t"><b>{{ title(n) }}</b>\
      <span class="muted"> {{ configured(n) ? "is connected" : "" }}</span></div>\
      <button @click="open = open === n ? \'\' : n">{{ open === n ? "Hide" : (configured(n) ? "Change" : "Set up") }}</button></div>\
    <template v-for="n in stores" :key="n + \'f\'"><store-form v-if="open === n" :store="n" @done="open = \'\'; $root.loadPlugins(); load()"></store-form></template>\
  </template>\
</div>',
    computed: {},
    methods: {
      when: function (iso) { var d = new Date(iso); return isNaN(d) ? iso : d.toLocaleString(); },
      title: function (n) { return STORES[n].title; },
      configured: function (n) { return LU.present(n); },
      call: function (command, args) { return LU.api('/api/plugin', { plugin: 'backup', command: command, args: args }); },
      itemsFor: function (tg) { return tg.kind === 'server' ? this.st.server_items : this.st.project_items; },
      itemName: function (it) { return { caddy: 'web server (Caddy)', lite: 'settings of lite', settings: 'settings', secrets: 'secrets', database: 'database', data: 'files (data volumes)' }[it] || it; },
      saveTarget: function (tg) {
        var self = this, e = this.edit[tg.name], args = [tg.name, e.enabled ? '--on' : '--off'];
        if (!e.items.length) { LU.say('err', 'Tick at least one thing to back up, or switch “back it up” off.'); return; }
        args.push('--items', e.items.join(','));
        if (e.stores.length) args.push('--stores', e.stores.join(',')); else args.push('--all-stores');
        var at = (e.at || '').trim();
        if (at && !/^([01][0-9]|2[0-3]):[0-5][0-9]$/.test(at)) { LU.say('err', 'The time is HH:MM, for example 02:30.'); return; }
        if (at) args.push('--at', at); else args.push('--no-time');
        LU.run('Saving…', '/api/plugin', { plugin: 'backup', command: 'set-target', args: args }, function () { return 'Saved the backup setting of ' + (tg.kind === 'server' ? 'the server' : tg.name) + '.'; }).then(function () { self.load(); });
      },
      runTarget: function (tg) {
        var self = this, args = tg.kind === 'server' ? ['--server'] : ['--project', tg.name];
        LU.run('Taking a backup…', '/api/plugin', { plugin: 'backup', command: 'run', args: args }, function () { return 'A backup was taken.'; }).then(function () { self.load(); });
      },
      fillEdit: function () {
        var self = this, e = {};
        ((this.st && this.st.targets) || []).forEach(function (tg) {
          e[tg.name] = { enabled: tg.enabled, items: (tg.items || []).slice(), stores: (tg.stores || []).slice(), at: tg.at || '' };
        });
        this.edit = e;
      },
      load: function () {
        var self = this; self.error = '';
        self.call('status', []).then(function (r) { if (r.ok) { self.st = r.data; self.fillEdit(); } else self.error = r.error ? r.error.message : 'Could not read the state.'; });
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
