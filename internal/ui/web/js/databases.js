(function () {
  function plain(r) { return r.ok ? r.data : null; }
  LU.components.Databases = {
    data: function () { return { st: null, dbs: null, backups: null, error: '', more: false, askKey: null, key: '' }; },
    template: '\
<div>\
  <h1>Databases</h1>\
  <p class="sub">PostgreSQL on this server: the databases of your projects and their backups.</p>\
  <p v-if="error" class="down">{{ error }}</p>\
  <div v-if="st" class="card row spread">\
    <div><b>PostgreSQL</b> <span class="muted">{{ st.version }}, {{ st.active ? "running" : "not running" }}</span><div class="help">Daily backup of the databases: {{ st.scheduled ? "on" : "off" }}</div></div>\
    <button @click="toggleSchedule" :disabled="$root.state.busy">{{ st.scheduled ? "Turn daily backup off" : "Turn daily backup on" }}</button>\
  </div>\
  <h3>Databases</h3>\
  <p v-if="dbs && !dbs.length" class="empty">No databases yet. A project that needs one creates it for you.</p>\
  <div v-for="d in dbs" :key="d" class="notice"><icon name="database"></icon><div class="t"><b>{{ d }}</b></div>\
    <button @click="backup(d)" :disabled="$root.state.busy"><icon name="backup"></icon>Back up now</button>\
    <button v-if="d !== \'postgres\'" @click="drop(d)" :disabled="$root.state.busy"><icon name="trash"></icon>Delete</button></div>\
  <h3>Backups</h3>\
  <p v-if="backups && !backups.length" class="empty">No backups yet. Press “Back up now” next to a database.</p>\
  <table v-if="backups && backups.length"><tr><th>Database</th><th>Taken</th><th></th></tr>\
    <tr v-for="b in backups" :key="b.file"><td>{{ b.database }}</td><td>{{ when(b.at) }}</td>\
      <td><button @click="restore(b)" :disabled="$root.state.busy">Restore</button></td></tr></table>\
  <div v-if="askKey" class="card"><b>This backup is encrypted.</b> Paste your backup key (the file you saved when you set up backups).\
    <textarea v-model="key" spellcheck="false" placeholder="AGE-SECRET-KEY-…"></textarea>\
    <div class="row" style="margin-top:8px"><button class="primary" @click="restoreWithKey" :disabled="$root.state.busy">Restore</button><button @click="askKey = null; key = \'\'">Cancel</button></div></div>\
  <h3>More</h3>\
  <button @click="more = !more">{{ more ? "Hide" : "Show all database commands" }}</button>\
  <plugin-panel v-if="more && plugin" :plugin="plugin" @done="load"></plugin-panel>\
</div>',
    computed: { plugin: function () { return LU.state.plugins.filter(function (p) { return p.name === 'pg'; })[0]; } },
    methods: {
      when: function (iso) { var d = new Date(iso); return isNaN(d) ? iso : d.toLocaleString(); },
      call: function (command, args, stdin) {
        var body = { plugin: 'pg', command: command, args: args };
        if (stdin) body.stdin = stdin;
        return LU.api('/api/plugin', body);
      },
      load: function () {
        var self = this; self.error = '';
        self.call('status', []).then(function (r) { self.st = plain(r); if (!r.ok) self.error = r.error ? r.error.message : ''; });
        self.call('databases', []).then(function (r) { self.dbs = r.ok ? (r.data.databases || []) : []; });
        self.call('backups', []).then(function (r) { self.backups = r.ok ? (r.data.backups || []) : []; });
      },
      run: function (wait, command, args, ok, stdin) {
        var self = this, body = { plugin: 'pg', command: command, args: args };
        if (stdin) body.stdin = stdin;
        return LU.run(wait, '/api/plugin', body, function () { return ok; }).then(function (r) { self.load(); return r; });
      },
      toggleSchedule: function () {
        var on = !this.st.scheduled;
        this.run(on ? 'Turning on…' : 'Turning off…', 'schedule', [on ? 'on' : 'off'], on ? 'Daily backups of the databases are on.' : 'Daily backups of the databases are off.');
      },
      backup: function (d) { this.run('Backing up ' + d + '…', 'backup', [d], 'A backup of ' + d + ' was taken.'); },
      drop: function (d) {
        var typed = prompt('This deletes the database ' + d + ' and everything in it. Type its name to confirm:');
        if (typed !== d) { if (typed !== null) LU.say('err', 'The name did not match, nothing was deleted.'); return; }
        this.run('Deleting…', 'drop-db', [d], 'The database ' + d + ' was deleted.');
      },
      restore: function (b) {
        var typed = prompt('This replaces the current data of ' + b.database + ' with the backup. Type the database name to confirm:');
        if (typed !== b.database) { if (typed !== null) LU.say('err', 'The name did not match, nothing was changed.'); return; }
        if (/\.age$/.test(b.file)) { this.askKey = b; return; }
        this.run('Restoring…', 'restore', [b.database, b.file], 'The database ' + b.database + ' was restored.');
      },
      restoreWithKey: function () {
        var b = this.askKey, k = this.key.trim(), self = this;
        if (!k) { LU.say('err', 'Paste the backup key first.'); return; }
        this.run('Restoring…', 'restore', [b.database, b.file, '--identity-stdin'], 'The database ' + b.database + ' was restored.', k + '\n').then(function () { self.askKey = null; self.key = ''; });
      }
    },
    mounted: function () { this.load(); }
  };
})();
