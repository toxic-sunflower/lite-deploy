(function () {
  // Server lists what is installed and what can be added. The data comes from the plugins the server reports.
  LU.components.Server = {
    data: function () { return { open: '' }; },
    template: '\
<div>\
  <h1>Server</h1>\
  <p class="sub">What is installed on this server and what you can add.</p>\
  <h3>lite</h3>\
  <div class="notice"><div class="t" v-if="$root.state.update">Version {{ $root.state.update.current }}.\
      {{ $root.state.update.available ? "A new version is available: " + $root.state.update.latest + "." : "This is the latest version." }}</div>\
    <div class="t muted" v-else>Checking for updates…</div>\
    <button v-if="$root.state.update && $root.state.update.available" class="primary" @click="$root.applyUpdate" :disabled="$root.state.busy">Update</button>\
    <button v-else @click="$root.checkUpdate">Check again</button></div>\
  <h3>Installed</h3>\
  <div v-for="p in installed" :key="p.name">\
    <div class="notice"><div class="t"><b>{{ p.name }}</b> <span class="muted">{{ p.description }}</span></div>\
      <button @click="open = open === p.name ? \'\' : p.name">{{ open === p.name ? "Hide" : "Manage" }}</button></div>\
    <plugin-panel v-if="open === p.name" :plugin="p" @done="reload"></plugin-panel>\
  </div>\
  <h3>Can be added</h3>\
  <p v-if="!available.length" class="muted">Everything available is already installed.</p>\
  <div v-for="p in available" :key="p.name" class="notice">\
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
      hasInstall: function (p) { return (p.commands || []).some(function (c) { return c.name === 'install' && !c.args; }); },
      reload: function () { LU.loadPlugins(); },
      install: function (p) {
        if (!confirm('Install ' + p.name + ' on this server?')) return;
        LU.run('Installing ' + p.name + '…', '/api/plugin', { plugin: p.name, command: 'install', args: [] }, function () { return p.name + ' is installed. Next: create a project in Projects.'; })
          .then(function () { LU.loadPlugins(); });
      }
    }
  };
})();
