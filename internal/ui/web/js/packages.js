(function () {
  // The packages of the server: search, install, update and remove. Everything is done by the `package` plugin on the
  // server; this page only asks.
  LU.components.Packages = {
    data: function () { return { query: '', found: null, list: null, error: '' }; },
    template: '\
<div>\
  <h1>Packages</h1>\
  <p class="sub">Programs installed on this server. Search for one, install it, or remove one that you installed here.</p>\
  <h3>Find and install</h3>\
  <form class="row" @submit.prevent="search">\
    <input class="grow" type="text" v-model="query" placeholder="What do you need? For example: redis, git, ffmpeg" autocomplete="off" spellcheck="false">\
    <button class="primary" type="submit" :disabled="$root.state.busy"><icon name="plus"></icon>Search</button></form>\
  <p v-if="error" class="down">{{ error }}</p>\
  <table v-if="found && found.length"><tr><th>Package</th><th>What it is</th><th></th></tr>\
    <tr v-for="f in found" :key="f.name"><td><code>{{ f.name }}</code></td><td>{{ f.summary }}</td>\
      <td style="text-align:right"><button @click="install(f.name)" :disabled="$root.state.busy">Install</button></td></tr></table>\
  <p v-else-if="found" class="empty">Nothing found. Try another word.</p>\
  <h3>Installed through Lite Deploy</h3>\
  <p v-if="list && !list.length" class="empty">Nothing yet. Programs you install here appear in this list.</p>\
  <table v-else-if="list"><tr><th>Package</th><th>Version</th><th>Installed</th><th></th></tr>\
    <tr v-for="p in list" :key="p.name"><td><code>{{ p.name }}</code></td><td>{{ p.version }}</td><td class="muted">{{ p.installed_at }}</td>\
      <td style="text-align:right;white-space:nowrap"><button @click="update(p.name)" :disabled="$root.state.busy">Update</button>\
        <button @click="remove(p.name)" :disabled="$root.state.busy">Remove</button></td></tr></table>\
  <p v-else class="empty">Loading…</p>\
  <div class="help">Only programs installed here are listed. The ones that keep the server working (SSH, sudo, systemd) cannot be removed.</div>\
</div>',
    methods: {
      load: function () {
        var self = this;
        LU.api('/api/plugin', { plugin: 'package', command: 'list', args: [] }).then(function (r) {
          if (r.ok) { self.list = (r.data && r.data.packages) || []; } else { self.error = r.error ? r.error.message : 'Could not load.'; }
        });
      },
      search: function () {
        var self = this, q = this.query.trim();
        self.error = '';
        if (q.length < 2) { LU.say('err', 'Type at least two letters.'); return; }
        LU.run('Searching…', '/api/plugin', { plugin: 'package', command: 'search', args: q.split(/\s+/) }, function () { return 'Done.'; })
          .then(function (r) { if (r.ok) { self.found = (r.data && r.data.found) || []; LU.clear(); } });
      },
      act: function (verb, name, wait, doneText) {
        var self = this;
        if (!confirm(verb[0].toUpperCase() + verb.slice(1) + ' ' + name + ' on this server?')) return;
        LU.run(wait, '/api/plugin', { plugin: 'package', command: verb, args: [name] }, function () { return doneText; }).then(function () { self.load(); });
      },
      install: function (n) { this.act('install', n, 'Installing ' + n + '…', n + ' is installed.'); },
      update: function (n) { this.act('update', n, 'Updating ' + n + '…', n + ' is updated.'); },
      remove: function (n) { this.act('remove', n, 'Removing ' + n + '…', n + ' is removed.'); }
    },
    mounted: function () { this.load(); }
  };
})();
