(function () {
  LU.components.Overview = {
    data: function () { return { verify: null, projects: null, error: '' }; },
    template: '\
<div>\
  <h1>Overview</h1>\
  <p class="sub">{{ $root.state.server }}</p>\
  <p v-if="error" class="down">{{ error }}</p>\
  <template v-if="verify">\
    <div class="row spread" style="margin-bottom:14px">\
      <span class="pill" :class="health.cls">{{ health.text }}</span>\
      <button @click="load" :disabled="$root.state.busy">Refresh</button>\
    </div>\
    <div class="metrics">\
      <div class="metric"><div class="l">Projects</div><div class="v">{{ projects ? projects.length : 0 }}</div></div>\
      <div class="metric"><div class="l">Checks passed</div><div class="v">{{ verify.ok }} of {{ verify.ok + verify.warnings + verify.failures }}</div></div>\
      <div class="metric"><div class="l">Protection</div><div class="v">{{ protectionOn ? "On" : "Needs work" }}</div></div>\
    </div>\
    <div v-if="$root.state.update && $root.state.update.available" class="notice warn">\
      <div class="t">A new version of lite is available: {{ $root.state.update.latest }} (you have {{ $root.state.update.current }}).</div>\
      <button class="primary" @click="$root.applyUpdate" :disabled="$root.state.busy">Update</button>\
    </div>\
    <h3>Needs your attention</h3>\
    <div v-for="f in attention" :key="f.plugin + f.check" class="notice" :class="f.level === \'fail\' ? \'bad\' : \'warn\'">\
      <div class="t">{{ f.message }}<div class="help" v-if="f.fix">{{ f.fix }}</div></div>\
    </div>\
    <div v-if="!attention.length && !hints.length" class="empty">Nothing needs your attention.</div>\
    <div v-for="h in hints" :key="h.text" class="notice">\
      <div class="t">{{ h.text }}</div><button @click="$root.go(h.page)">{{ h.button }}</button>\
    </div>\
    <button v-if="fixable" @click="fix" :disabled="$root.state.busy">Fix what can be fixed automatically</button>\
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
      attention: function () { return this.verify ? this.verify.findings.filter(function (f) { return f.level !== 'ok' && f.plugin !== 'lite'; }) : []; },
      health: function () {
        if (!this.verify) return { cls: 'warn', text: '…' };
        if (this.verify.failures) return { cls: 'bad', text: 'Needs attention' };
        if (this.verify.warnings) return { cls: 'warn', text: 'Mostly fine' };
        return { cls: 'ok', text: 'All good' };
      },
      protectionOn: function () {
        return this.verify && !this.verify.findings.some(function (f) { return f.plugin === 'security' && f.level === 'fail'; });
      },
      fixable: function () { return this.attention.some(function (f) { return f.fix; }); },
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
      fix: function () {
        var self = this;
        if (!confirm('Apply the safe fixes? The firewall, users and keys are not touched.')) return;
        LU.run('Fixing…', '/api/fix', {}, function (d) { return d.applied.length ? 'Done: ' + d.applied.join('; ') : 'Nothing to fix automatically.'; }).then(function () { self.load(); });
      }
    },
    mounted: function () { this.load(); LU.checkUpdate(); }
  };
})();
