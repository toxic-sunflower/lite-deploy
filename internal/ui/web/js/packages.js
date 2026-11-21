(function () {
  // The packages of the server: search and install, see what is installed and what has an update, update or remove the
  // ones you choose, or update everything. All of it is done by the `package` plugin on the server; this page only asks.
  LU.components.Packages = {
    data: function () { return { query: '', found: null, list: null, error: '', all: false, filter: '', onlyUpd: false, sel: {} }; },
    template: '\
<div>\
  <h1>Packages</h1>\
  <p class="sub">Programs installed on this server: find and install one, see which have an update, update or remove the ones you choose.</p>\
  <h3>Find and install <tip text="Search the repositories of the system by name or by what the program does, then install it."></tip></h3>\
  <form class="row" @submit.prevent="search">\
    <input class="grow" type="text" v-model="query" placeholder="What do you need? For example: redis, git, ffmpeg" autocomplete="off" spellcheck="false">\
    <button class="primary" type="submit" :disabled="$root.state.busy"><icon name="plus"></icon>Search</button></form>\
  <table v-if="found && found.length"><tr><th>Package</th><th>What it is</th><th></th></tr>\
    <tr v-for="f in found" :key="f.name"><td><code>{{ f.name }}</code></td><td>{{ f.summary }}</td>\
      <td style="text-align:right"><button @click="install(f.name)" :disabled="$root.state.busy">Install</button></td></tr></table>\
  <p v-else-if="found" class="empty">Nothing found. Try another word.</p>\
  <h3>Installed <tip text="Programs on the server. A yellow line means a newer version is available."></tip></h3>\
  <div class="row" style="margin-bottom:8px">\
    <label class="check" style="margin:0"><input type="checkbox" v-model="all" @change="load"> Show all installed packages (not only those installed here)</label>\
    <label class="check" style="margin:0"><input type="checkbox" v-model="onlyUpd"> Only those with an update</label>\
    <input class="grow" type="text" v-model="filter" placeholder="Filter by name" autocomplete="off" spellcheck="false"></div>\
  <div v-if="list" class="notice" :class="{warn: updates > 0}"><icon name="refresh"></icon>\
    <div class="t">{{ updates ? updates + (updates === 1 ? " package has" : " packages have") + " an update." : "Everything shown is up to date." }}</div>\
    <button v-if="updates" class="primary" @click="updateAll" :disabled="$root.state.busy">Update all</button></div>\
  <div class="row" style="margin:8px 0" v-if="list">\
    <button @click="updateSelected" :disabled="$root.state.busy || !chosen.length">Update selected ({{ chosen.length }})</button>\
    <button @click="removeSelected" :disabled="$root.state.busy || !chosen.length">Remove selected</button>\
    <span class="help" v-if="chosen.length > 16">Choose at most 16 at a time.</span>\
    <span class="help" v-else-if="chosen.length && !removable">Remove works only for packages installed here.</span></div>\
  <p v-if="error" class="down">{{ error }}</p>\
  <table v-if="list && shown.length"><tr><th></th><th>Package</th><th>Version</th><th>Update</th><th></th></tr>\
    <tr v-for="p in shown" :key="p.name" :style="p.update ? \'background:var(--warnbg)\' : \'\'">\
      <td><input type="checkbox" v-model="sel[p.name]"></td>\
      <td><code>{{ p.name }}</code></td><td>{{ p.version }}</td>\
      <td><span v-if="p.update" class="pill warn">{{ p.update }} available</span></td>\
      <td class="muted">{{ p.lite ? "installed here" : "" }}</td></tr></table>\
  <p v-else-if="list" class="empty">{{ list.length ? "Nothing matches the filter." : "Nothing yet. Programs you install here appear in this list; tick “Show all installed packages” to see everything." }}</p>\
  <p v-else class="empty">Loading…</p>\
  <div class="help">Updates are those the server already knows about (it refreshes its lists once a day). “Update all” refreshes them first. Packages that keep the server working (SSH, sudo, systemd) cannot be removed.</div>\
</div>',
    computed: {
      shown: function () {
        var f = this.filter.trim().toLowerCase(), only = this.onlyUpd;
        return (this.list || []).filter(function (p) { return (!f || p.name.toLowerCase().indexOf(f) >= 0) && (!only || p.update); });
      },
      updates: function () { return (this.list || []).filter(function (p) { return p.update; }).length; },
      chosen: function () { var s = this.sel; return (this.list || []).filter(function (p) { return s[p.name]; }); },
      removable: function () { return this.chosen.every(function (p) { return p.lite; }); }
    },
    methods: {
      load: function () {
        var self = this; self.error = ''; self.list = null; self.sel = {};
        LU.api('/api/plugin', { plugin: 'package', command: 'list', args: self.all ? ['--all'] : [] }).then(function (r) {
          if (r.ok) { self.list = (r.data && r.data.packages) || []; } else { self.error = r.error ? r.error.message : 'Could not load.'; }
        });
      },
      search: function () {
        var self = this, q = this.query.trim();
        if (q.length < 2) { LU.say('err', 'Type at least two letters.'); return; }
        LU.run('Searching…', '/api/plugin', { plugin: 'package', command: 'search', args: q.split(/\s+/) }, function () { return 'Done.'; })
          .then(function (r) { if (r.ok) { self.found = (r.data && r.data.found) || []; LU.clear(); } });
      },
      act: function (verb, names, ask, wait, doneText) {
        var self = this;
        if (!confirm(ask)) return;
        LU.run(wait, '/api/plugin', { plugin: 'package', command: verb, args: names }, function () { return doneText; }).then(function () { self.load(); });
      },
      install: function (n) { this.act('install', [n], 'Install ' + n + ' on this server?', 'Installing ' + n + '…', n + ' is installed.'); },
      updateAll: function () { this.act('update', ['--all'], 'Update every package that has an update (' + this.updates + ')? Some services may restart.', 'Updating everything… this can take a few minutes', 'Everything is updated.'); },
      updateSelected: function () {
        var n = this.chosen.map(function (p) { return p.name; });
        if (n.length > 16) { LU.say('err', 'Choose at most 16 packages at a time, or use “Update all”.'); return; }
        this.act('update', n, 'Update ' + n.join(', ') + '?', 'Updating…', 'Updated: ' + n.join(', ') + '.');
      },
      removeSelected: function () {
        var n = this.chosen.map(function (p) { return p.name; });
        if (n.length > 16) { LU.say('err', 'Choose at most 16 packages at a time.'); return; }
        if (!this.removable) { LU.say('err', 'Remove works only for packages installed here. Untick the others.'); return; }
        this.act('remove', n, 'Remove ' + n.join(', ') + ' from this server?', 'Removing…', 'Removed: ' + n.join(', ') + '.');
      }
    },
    mounted: function () { this.load(); }
  };
})();
