(function () {
  function newService() {
    return { name: 'api', kind: 'quadlet', image: '', cport: '8080', exec: '', rurl: '', rkey: '', rtok: '', mem: '512m', health: '/health/ready', env: '', sec: '', dbenv: '' };
  }
  function newSite() { return { name: 'main', host: '', service: 'api', publish: 'tunnel', proxy: 'none', body: '5MB' }; }
  function lines(t) { return t.split('\n').map(function (l) { return l.trim(); }).filter(Boolean); }
  function pair(l) { var i = l.indexOf('='); return i < 1 ? null : [l.slice(0, i).trim(), l.slice(i + 1)]; }
  function secretName(parts) { var n = parts.join('_').toLowerCase().replace(/[^a-z0-9_]/g, '_'); return /^[a-z]/.test(n) ? n : 's_' + n; }
  function opt(o, k, v) { if (v !== '' && v !== undefined && v !== null) o[k] = v; }

  LU.components.NewProject = {
    data: function () {
      return {
        name: '', desc: '', dbOn: false, db: { server: '', port: '5432', fmt: 'url', pass: '' },
        regOn: false, reg: { host: 'ghcr.io', user: '', token: '' },
        services: [newService()], sites: [newSite()], key: '', hash: '', plan: null, planErr: null
      };
    },
    template: '\
<div class="card">\
  <h2>New project</h2>\
  <div v-if="!podman" class="notice warn"><div class="t">Container components need podman, which is not on this server yet. Add it in <b>Server</b> first.</div></div>\
  <div class="row"><div class="grow"><label>Project name (Latin letters, digits and “-”)</label><input type="text" v-model="name" placeholder="shop" autocomplete="off" spellcheck="false"></div>\
    <div class="grow"><label>Description (optional)</label><input type="text" v-model="desc" autocomplete="off"></div></div>\
  <label class="check"><input type="checkbox" v-model="dbOn"> The project needs a PostgreSQL database</label>\
  <div class="box" v-if="dbOn">\
    <div class="row"><div class="grow"><label>Database server (empty: this server)</label><input type="text" v-model="db.server" autocomplete="off"></div>\
      <div class="grow"><label>Port (for another server)</label><input type="text" v-model="db.port"></div>\
      <div class="grow"><label>Connection string format</label><select v-model="db.fmt"><option value="url">url</option><option value="npgsql">npgsql (.NET)</option><option value="libpq">libpq</option></select></div></div>\
    <label>Password of the database role (only for a database on another server)</label><input type="password" v-model="db.pass" autocomplete="off">\
  </div>\
  <label class="check"><input type="checkbox" v-model="regOn"> The container image is in a private registry</label>\
  <div class="box" v-if="regOn">\
    <div class="row"><div class="grow"><label>Registry</label><input type="text" v-model="reg.host"></div><div class="grow"><label>User</label><input type="text" v-model="reg.user" autocomplete="off"></div></div>\
    <label>Read-only token</label><input type="password" v-model="reg.token" autocomplete="off">\
  </div>\
  <h3>Components</h3>\
  <div class="box" v-for="(s, i) in services" :key="i">\
    <div class="row"><div class="grow"><label>Name</label><input type="text" v-model="s.name" autocomplete="off"></div>\
      <div class="grow"><label>How it runs</label><select v-model="s.kind"><option value="quadlet">Container (podman)</option><option value="unit">Program on the server</option></select></div></div>\
    <div v-if="s.kind === \'quadlet\'" class="row">\
      <div class="grow"><label>Container image, without the version</label><input type="text" v-model="s.image" placeholder="ghcr.io/you/app" autocomplete="off" spellcheck="false"></div>\
      <div class="grow"><label>Port the application listens on</label><input type="text" v-model="s.cport"></div></div>\
    <template v-else>\
      <label>Command to start it (arguments separated by spaces)</label><input type="text" v-model="s.exec" placeholder="./bin/app" autocomplete="off">\
      <label>Where the build is downloaded from (a .tar.gz link; {sha} becomes the version)</label><input type="text" v-model="s.rurl" placeholder="https://example.com/app-{sha}.tar.gz" autocomplete="off">\
      <label>Public key that your build is signed with (minisign)</label><input type="text" v-model="s.rkey" placeholder="RWQ…" autocomplete="off">\
      <label>Download token (optional)</label><input type="password" v-model="s.rtok" autocomplete="off">\
    </template>\
    <div class="row"><div class="grow"><label>Memory limit</label><input type="text" v-model="s.mem"></div>\
      <div class="grow"><label>Address that answers “I am ready” after start</label><input type="text" v-model="s.health"></div></div>\
    <label>Settings (one per line: NAME=value)</label><textarea v-model="s.env" placeholder="ENVIRONMENT=Production" spellcheck="false"></textarea>\
    <label>Secrets (one per line: NAME=value; kept encrypted on the server)</label><textarea v-model="s.sec" spellcheck="false"></textarea>\
    <label>Setting that receives the database address (optional)</label><input type="text" v-model="s.dbenv" placeholder="DATABASE_URL" autocomplete="off">\
    <div style="margin-top:10px"><button type="button" @click="services.splice(i, 1)" v-if="services.length > 1">Remove this component</button></div>\
  </div>\
  <button type="button" @click="services.push(newService())">Add a component</button>\
  <h3>Web addresses</h3>\
  <div class="box" v-for="(s, i) in sites" :key="i">\
    <div class="row"><div class="grow"><label>Name</label><input type="text" v-model="s.name" autocomplete="off"></div>\
      <div class="grow"><label>Domain</label><input type="text" v-model="s.host" placeholder="shop.example.com" autocomplete="off"></div>\
      <div class="grow"><label>Which component answers</label><input type="text" v-model="s.service" autocomplete="off"></div></div>\
    <div class="row"><div class="grow"><label>How visitors reach it</label><select v-model="s.publish"><option value="tunnel">Through a Cloudflare Tunnel</option><option value="direct">Directly (Caddy gets the certificate itself)</option></select></div>\
      <div class="grow"><label>Traffic comes through Cloudflare</label><select v-model="s.proxy"><option value="none">No</option><option value="cloudflare">Yes</option></select></div>\
      <div class="grow"><label>Largest request</label><input type="text" v-model="s.body"></div></div>\
    <div style="margin-top:10px"><button type="button" @click="sites.splice(i, 1)">Remove this address</button></div>\
  </div>\
  <button type="button" @click="sites.push(newSite())">Add an address</button>\
  <label>Key for deploying from CI (optional, ssh-ed25519)</label><textarea v-model="key" placeholder="ssh-ed25519 AAAA… github-actions" spellcheck="false"></textarea>\
  <div class="row" style="margin-top:14px">\
    <button class="primary" type="button" @click="preview" :disabled="$root.state.busy">Show what will happen</button>\
    <button type="button" @click="create" :disabled="$root.state.busy || !hash">Create project</button></div>\
  <div v-if="planErr" style="margin-top:10px"><p class="down">{{ planErr.text }}</p><div v-for="d in planErr.details" class="warntxt">{{ d.path }}: {{ d.message }}</div></div>\
  <table v-if="plan"><tr><th>Step</th><th>What</th><th>Result</th></tr>\
    <tr v-for="s in plan" :key="s.id"><td>{{ s.id }}</td><td>{{ s.action }}</td><td :class="s.state === \'failed\' ? \'down\' : \'\'">{{ s.state }} {{ s.detail }} {{ s.error }}</td></tr></table>\
</div>',
    computed: {
      podman: function () { return LU.present('podman'); },
      snapshot: function () { return JSON.stringify([this.name, this.desc, this.dbOn, this.db, this.regOn, this.reg, this.services, this.sites, this.key]); }
    },
    watch: { snapshot: function () { this.hash = ''; } },
    methods: {
      newService: newService, newSite: newSite,
      // build makes the project description and the secret values; it returns a string if the form has an error
      build: function () {
        var p = { schema: 'lite-deploy/project/v2', name: this.name.trim() }, sec = {}, errs = [];
        opt(p, 'description', this.desc.trim());
        if (this.dbOn) {
          p.database = { engine: 'postgres', connection_format: this.db.fmt };
          if (this.db.server.trim()) {
            p.database.server = this.db.server.trim();
            if (this.db.port.trim()) p.database.port = parseInt(this.db.port, 10);
            p.database.password_secret = 'secret://db_password'; sec.db_password = this.db.pass;
          }
        }
        if (this.regOn) {
          p.registry = { host: this.reg.host.trim(), username: this.reg.user.trim(), token_secret: 'secret://registry_token' };
          sec.registry_token = this.reg.token;
        }
        p.services = {};
        this.services.forEach(function (s) {
          var n = s.name.trim(), o = {};
          if (s.kind === 'unit') {
            o.kind = 'unit'; o.exec = s.exec.trim().split(/\s+/).filter(Boolean);
            o.release = { url: s.rurl.trim(), minisign_key: s.rkey.trim() };
            if (s.rtok) { var rn = secretName([n, 'release_token']); o.release.token_secret = 'secret://' + rn; sec[rn] = s.rtok; }
          } else { o.image = s.image.trim(); o.container_port = parseInt(s.cport, 10) || 0; }
          var lim = {}; opt(lim, 'memory', s.mem.trim()); if (Object.keys(lim).length) o.limits = lim;
          if (s.health.trim()) o.health = { path: s.health.trim() };
          var env = {};
          lines(s.env).forEach(function (l) { var kv = pair(l); if (kv) env[kv[0]] = kv[1]; else errs.push('Setting "' + l + '": write it as NAME=value'); });
          if (Object.keys(env).length) o.env = env;
          var sc = {};
          lines(s.sec).forEach(function (l) { var kv = pair(l); if (kv) { var sn = secretName([n, kv[0]]); sc[kv[0]] = 'secret://' + sn; sec[sn] = kv[1]; } else errs.push('Secret "' + l.split('=')[0] + '": write it as NAME=value'); });
          if (Object.keys(sc).length) o.secrets = sc;
          opt(o, 'database_env', s.dbenv.trim());
          p.services[n] = o;
        });
        p.sites = this.sites.map(function (s) {
          var o = { name: s.name.trim(), host: s.host.trim(), service: s.service.trim(), publish: s.publish, trusted_proxy: s.proxy };
          opt(o, 'max_body', s.body.trim()); return o;
        });
        if (this.key.trim()) p.deploy_key = { public_key: this.key.trim() };
        return errs.length ? errs.join('\n') : { project: p, secrets: sec };
      },
      send: function (path, wait) {
        var self = this, b = this.build();
        if (typeof b === 'string') { LU.say('err', b); return Promise.resolve(null); }
        var body = { project: b.project, secrets: b.secrets };
        if (path.indexOf('create') >= 0 && this.hash) body.expect_project_hash = this.hash;
        return LU.run(wait, path, body).then(function (r) {
          self.plan = null; self.planErr = null;
          if (r.ok) { self.plan = r.data.steps || []; } else { self.planErr = { text: LU.errText(r), details: (r.error && r.error.details) || [] }; LU.clear(); }
          return r;
        });
      },
      preview: function () {
        var self = this; this.hash = '';
        this.send('/api/project/preview', 'Building the plan…').then(function (r) {
          if (r && r.ok) { self.hash = r.data.project_hash; LU.say('ok', 'Nothing has changed on the server yet. Check the steps below, then press “Create project”.'); }
        });
      },
      create: function () {
        var self = this;
        if (!this.hash || !confirm('Create project ' + this.name.trim() + ' on the server with the steps shown?')) return;
        this.send('/api/project/create', 'Creating the project, this can take a few minutes…').then(function (r) {
          if (r && r.ok) { LU.say('ok', 'Project created. Now deploy a version from its card.'); self.hash = ''; self.$emit('created'); }
        });
      }
    }
  };
})();
