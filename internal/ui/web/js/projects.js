(function () {
  LU.components.ProjectCard = {
    props: ['p'],
    data: function () { return { sha: '' }; },
    template: '\
<div class="card">\
  <div class="row spread"><h2><icon name="apps"></icon> {{ p.project }}</h2><span class="muted">{{ p.active === "none" ? "not deployed yet" : "active slot: " + p.active }}</span></div>\
  <table>\
    <tr><th>Slot</th><th>Component</th><th>Port</th><th>State</th></tr>\
    <template v-for="slot in [\'blue\', \'green\']">\
      <tr v-for="(s, svc) in (p.slots[slot] || {})" :key="slot + svc">\
        <td :style="slot === p.active ? \'font-weight:600\' : \'\'">{{ slot }}{{ slot === p.active ? " (active)" : "" }}</td>\
        <td>{{ svc }}</td><td>{{ s.port }}</td>\
        <td :class="s.up ? \'up\' : \'down\'">{{ s.up ? "running" + (s.version ? ", version " + short(s.version) : "") : "not responding" }}</td>\
      </tr>\
    </template>\
  </table>\
  <form class="row" @submit.prevent="deploy">\
    <input class="grow" type="text" v-model="sha" maxlength="40" placeholder="Version to deploy: the full commit hash (40 characters)" autocomplete="off" spellcheck="false">\
    <button class="primary" type="submit" :disabled="$root.state.busy">Deploy</button>\
    <button type="button" @click="rollback" :disabled="$root.state.busy">Roll back</button>\
  </form>\
  <div class="hist" v-if="p.history && p.history.length"><div v-for="(x, i) in history" :key="i">{{ x.time }}  {{ x.event }}  {{ short(x.version) }}  slot {{ x.slot }}</div></div>\
</div>',
    computed: { history: function () { return this.p.history.slice().reverse(); } },
    methods: {
      short: LU.short,
      deploy: function () {
        var self = this, sha = this.sha.trim().toLowerCase();
        if (!/^[0-9a-f]{40}$/.test(sha)) { LU.say('err', 'The full commit hash is needed: 40 characters, 0-9 and a-f.'); return; }
        if (!confirm('Deploy ' + sha.slice(0, 12) + ' to ' + this.p.project + '?')) return;
        LU.run('Deploying, this can take a few minutes…', '/api/deploy', { project: this.p.project, sha: sha }, function (d) {
          return 'Deployed ' + LU.short(d.version) + ' to slot ' + d.slot + '. Traffic switched.';
        }).then(function () { self.$emit('changed'); });
      },
      rollback: function () {
        var self = this;
        if (!confirm('Roll ' + this.p.project + ' back to the previous version?')) return;
        LU.run('Rolling back…', '/api/rollback', { project: this.p.project }, function (d) {
          return 'Rolled back to ' + LU.short(d.version) + ' (slot ' + d.slot + ').';
        }).then(function () { self.$emit('changed'); });
      }
    }
  };

  LU.components.Projects = {
    data: function () { return { projects: null, error: '', creating: location.hash === '#projects/new' }; },
    template: '\
<div>\
  <div class="row spread"><div><h1>Projects</h1><p class="sub">Your applications on this server.</p></div>\
    <button class="primary" @click="creating = !creating"><icon :name="creating ? \'check\' : \'plus\'"></icon>{{ creating ? "Close" : "New project" }}</button></div>\
  <new-project v-if="creating" @created="created"></new-project>\
  <p v-if="error" class="down">{{ error }}</p>\
  <p v-if="projects && !projects.length && !creating" class="empty">No projects yet. Press “New project”.</p>\
  <div v-if="undeployed" class="notice"><icon name="info"></icon><div class="t">Next: paste the commit hash of the version you want to run into the project card and press Deploy.</div></div>\
  <project-card v-for="p in projects" :key="p.project" :p="p" @changed="load"></project-card>\
</div>',
    computed: { undeployed: function () { return !!this.projects && this.projects.some(function (p) { return p.active === 'none'; }); } },
    methods: {
      load: function () {
        var self = this; self.error = '';
        LU.api('/api/status').then(function (r) {
          if (r.ok) self.projects = ('projects' in r.data) ? (r.data.projects || []) : [r.data];
          else if (r.error && r.error.code === 'project_not_found') self.projects = [];
          else self.error = r.error ? r.error.message : 'Could not load projects.';
        });
      },
      created: function () { this.creating = false; this.load(); }
    },
    mounted: function () { this.load(); }
  };
})();
