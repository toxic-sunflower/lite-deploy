(function () {
  // Server lists what is installed and what can be added. The data comes from the plugins the server reports.
  LU.components.Server = {
    data: function () { return { open: '' }; },
    template: '\
<div>\
  <h1>Server</h1>\
  <p class="sub">What is installed on this server and what you can add.</p>\
  <h3>Lite Deploy <tip text="Lite Deploy itself on the server. Update installs the newest version; the page waits about a minute."></tip></h3>\
  <div class="notice"><icon name="refresh"></icon><div class="t" v-if="$root.state.update">Version {{ $root.state.update.current }}.\
      {{ $root.state.update.available ? "A new version is available: " + $root.state.update.latest + "." : "This is the latest version." }}</div>\
    <div class="t muted" v-else>Checking for updates…</div>\
    <button v-if="$root.state.update && $root.state.update.available" class="primary" @click="$root.applyUpdate" :disabled="$root.state.busy">Update</button>\
    <button v-else @click="$root.checkUpdate">Check again</button></div>\
  <h3>Server configuration <tip text="Save the setup of this server as a file, to set up another server the same way."></tip></h3>\
  <div class="card"><div class="row spread"><div><b>Save this server as a file</b><div class="help">Who may log in (with public keys), the firewall, the programs, protection and backups. No passwords or secrets. Give the file to <code>lite init --config</code> on a new server.</div></div>\
    <button @click="saveConfig" :disabled="$root.state.busy"><icon name="backup"></icon>Save configuration</button></div></div>\
  <h3>Installed <tip text="Plugins add abilities to the server (backups, a database, Cloudflare). Installed ones can be opened."></tip></h3>\
  <div v-for="p in installed" :key="p.name">\
    <div class="notice"><icon name="check"></icon><div class="t"><b>{{ p.name }}</b> <span class="muted">{{ p.description }}</span></div>\
      <button v-if="home(p)" @click="$root.go(home(p).id)">Open {{ home(p).title }}</button>\
      <button @click="open = open === p.name ? \'\' : p.name">{{ open === p.name ? "Hide" : "Manage" }}</button></div>\
    <plugin-panel v-if="open === p.name" :plugin="p" @done="reload"></plugin-panel>\
  </div>\
  <h3>Can be added <tip text="Plugins that are not installed yet. Press Install to add one."></tip></h3>\
  <p v-if="!available.length" class="muted">Everything available is already installed.</p>\
  <div v-for="p in available" :key="p.name" class="notice"><icon name="plus"></icon>\
    <div class="t"><b>{{ p.name }}</b> <span class="muted">{{ p.description }}</span></div>\
    <button v-if="hasInstall(p)" class="primary" @click="install(p)" :disabled="$root.state.busy">Install</button>\
    <button v-else @click="open = open === p.name ? \'\' : p.name">Set up</button>\
  </div>\
  <template v-for="p in available" :key="p.name"><plugin-panel v-if="open === p.name" :plugin="p" @done="reload"></plugin-panel></template>\
</div>',
    mounted: function () { LU.checkUpdate(); },
    computed: {
      installed: function () { return LU.state.plugins.filter(function (p) { return p.installed !== false; }); },
      available: function () { return LU.state.plugins.filter(function (p) { return p.installed === false; }); }
    },
    methods: {
      // plugins that have a page of their own: the page is the way to manage them
      home: function (p) {
        var m = { backup: ['backups', 'Backups'], firewall: ['security', 'Protection'], security: ['security', 'Protection'], package: ['packages', 'Packages'],
          caddy: ['ingress', 'Ingress'], cloudflare: ['ingress', 'Ingress'], pg: ['databases', 'Databases'] }[p.name];
        return m ? { id: m[0], title: m[1] } : null;
      },
      hasInstall: function (p) { return (p.commands || []).some(function (c) { return c.name === 'install' && !c.args; }); },
      reload: function () { LU.loadPlugins(); },
      saveConfig: function () {
        LU.api('/api/server/config').then(function (r) {
          if (!r.ok) { LU.say('err', LU.errText(r)); return; }
          var a = document.createElement('a');
          a.href = URL.createObjectURL(new Blob([JSON.stringify(r.data.config, null, 2) + '\n'], { type: 'application/json' }));
          a.download = 'server.lite-config.json'; document.body.appendChild(a); a.click(); a.remove();
          LU.say('ok', 'Saved server.lite-config.json. On a new server: lite init --config server.lite-config.json');
        });
      },
      install: function (p) {
        if (!confirm('Install ' + p.name + ' on this server?')) return;
        LU.run('Installing ' + p.name + '…', '/api/plugin', { plugin: p.name, command: 'install', args: [] }, function () { return p.name + ' is installed. Next: create a project in Projects.'; })
          .then(function () { LU.loadPlugins(); });
      }
    }
  };
})();
