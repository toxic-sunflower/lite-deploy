(function () {
  LU.components.Overview = {
    data: function () { return { verify: null, projects: null, error: '' }; },
    template: '\
<div>\
  <h1>Overview</h1>\
  <p class="sub">{{ $root.state.server }}</p>\
  <p v-if="error" class="down">{{ error }}</p>\
  <template v-if="verify">\
    <div class="row spread" style="margin-bottom:16px">\
      <span class="pill" :class="health.cls"><icon :name="health.cls === \'ok\' ? \'check\' : \'alert\'"></icon>{{ health.text }}</span>\
      <button @click="load" :disabled="$root.state.busy"><icon name="refresh"></icon>Refresh</button>\
    </div>\
    <div class="metrics">\
      <div class="metric"><div class="l">Projects</div><div class="v">{{ projects ? projects.length : 0 }}</div></div>\
      <div class="metric"><div class="l">Checks passed</div><div class="v">{{ verify.ok }} of {{ verify.ok + verify.warnings + verify.failures }}</div></div>\
      <div class="metric"><div class="l">Protection</div><div class="v">{{ protectionOn ? "On" : "Needs work" }}</div></div>\
    </div>\
    <div v-if="$root.state.uiUpdate" class="notice warn">\
      <icon name="refresh"></icon><div class="t">A new version of Lite Deploy UI is available: {{ $root.state.uiUpdate.latest }} (you have {{ $root.state.uiUpdate.current }}).</div>\
      <button class="primary" @click="$root.applyUiUpdate" :disabled="$root.state.busy">Update</button>\
    </div>\
    <div v-if="$root.state.update && $root.state.update.available" class="notice warn">\
      <icon name="refresh"></icon><div class="t">A new version of Lite Deploy on the server is available: {{ $root.state.update.latest }} (you have {{ $root.state.update.current }}).</div>\
      <button class="primary" @click="$root.applyUpdate" :disabled="$root.state.busy">Update</button>\
    </div>\
    <h3>Needs your attention</h3>\
    <div v-for="f in attention" :key="f.key" class="notice" :class="f.level === \'fail\' ? \'bad\' : \'warn\'">\
      <icon name="alert"></icon><div class="t">{{ f.text }}</div>\
      <button v-if="f.action" class="primary" @click="act(f)" :disabled="$root.state.busy">{{ f.action.label }}</button>\
      <button v-else-if="f.page" @click="$root.go(f.page)">{{ f.pageLabel }}</button>\
    </div>\
    <div v-if="!attention.length && !hints.length" class="empty">Nothing needs your attention.</div>\
    <div v-for="h in hints" :key="h.text" class="notice">\
      <icon name="info"></icon><div class="t">{{ h.text }}</div><button @click="$root.go(h.page)">{{ h.button }}</button>\
    </div>\
    <template v-if="notes.length"><h3>For your information</h3>\
      <div v-for="f in notes" :key="f.key" class="notice"><icon name="info"></icon><div class="t muted">{{ f.text }}</div></div></template>\
    <h3>Projects</h3>\
    <div v-if="projects && !projects.length" class="empty">No projects yet.</div>\
    <div v-for="p in projects" :key="p.project" class="notice">\
      <div class="t"><span class="dot" :class="running(p) ? \'ok\' : \'bad\'"></span><b>{{ p.project }}</b>\
        <span class="muted"> · {{ p.active === "none" ? "not deployed yet" : "slot " + p.active }}</span></div>\
      <button @click="$root.go(\'projects\')">Open</button>\
    </div>\
  </template>\
  <p v-else-if="!error" class="empty">Checking the server…</p>\
</div>',
    computed: {
      items: function () { return this.verify ? LU.explain(this.verify.findings) : []; },
      attention: function () { return this.items.filter(function (f) { return !f.info; }); },
      notes: function () { return this.items.filter(function (f) { return f.info; }); },
      health: function () {
        if (!this.verify) return { cls: 'warn', text: '…' };
        if (this.attention.some(function (f) { return f.level === 'fail'; })) return { cls: 'bad', text: 'Needs attention' };
        if (this.attention.length) return { cls: 'warn', text: 'Mostly fine' };
        return { cls: 'ok', text: 'All good' };
      },
      protectionOn: function () {
        return this.verify && !this.verify.findings.some(function (f) { return f.plugin === 'security' && f.level === 'fail'; });
      },
      hints: function () {
        var h = [];
        if (this.projects && !this.projects.length) {
          if (!LU.present('podman')) h.push({ text: 'To run container projects, add podman to this server.', button: 'Open Server', page: 'server' });
          h.push({ text: 'Create your first project.', button: 'New project', page: 'projects' });
        }
        return h;
      }
    },
    methods: {
      running: function (p) {
        var ok = true;
        ['blue', 'green'].forEach(function (slot) {
          Object.keys(p.slots[slot] || {}).forEach(function (s) { if (slot === p.active && !p.slots[slot][s].up) ok = false; });
        });
        return p.active !== 'none' && ok;
      },
      load: function () {
        var self = this; self.error = '';
        LU.api('/api/verify').then(function (r) {
          if (r.data) self.verify = r.data; else self.error = r.error ? r.error.message : 'Could not check the server.';
        });
        LU.api('/api/status').then(function (r) {
          if (r.ok) self.projects = ('projects' in r.data) ? (r.data.projects || []) : [r.data];
          else if (r.error && r.error.code === 'project_not_found') self.projects = [];
        });
      },
      act: function (f) {
        var self = this, a = f.action;
        if (!confirm(a.confirm)) return;
        LU.run('Working…', '/api/plugin', { plugin: a.plugin, command: a.command, args: a.args }, function () { return 'Done.'; }).then(function () { self.load(); });
      },
      fix: function () {
        var self = this;
        if (!confirm('Apply the safe fixes? The firewall, users and keys are not touched.')) return;
        LU.run('Fixing…', '/api/fix', {}, function (d) { return d.applied.length ? 'Done: ' + d.applied.join('; ') : 'Nothing to fix automatically.'; }).then(function () { self.load(); });
      }
    },
    mounted: function () { this.load(); LU.checkUpdate(); LU.checkUiUpdate(); }
  };
})();
