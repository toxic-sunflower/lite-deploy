(function () {
  var S = LU.state;
  var ALL = [
    { id: 'overview', title: 'Overview', icon: 'home', comp: 'overview' },
    { id: 'projects', title: 'Projects', icon: 'apps', comp: 'projects' },
    { id: 'secrets', title: 'Secrets', icon: 'lock', comp: 'shared-secrets' },
    { id: 'ingress', title: 'Ingress', icon: 'world', comp: 'ingress' },
    { id: 'databases', title: 'Databases', icon: 'database', plugin: 'pg', comp: 'databases' },
    { id: 'backups', title: 'Backups', icon: 'backup', plugin: 'backup', comp: 'backups' },
    { id: 'security', title: 'Protection', icon: 'shield', comp: 'security' },
    { id: 'server', title: 'Server', icon: 'settings', comp: 'server' }
  ];

  var Root = {
    template: '\
<div class="layout">\
  <aside class="side">\
    <div class="brand"><img src="/static/logo.svg" alt="">Lite Deploy</div>\
    <div class="srvbox"><icon name="server"></icon>\
      <select v-model="state.server" @change="pick" aria-label="Server"><option v-for="h in state.servers" :key="h" :value="h">{{ h }}</option></select></div>\
    <div class="srvbtns"><button @click="addServer"><icon name="plus"></icon>Add</button><button v-if="state.servers.length" @click="removeServer"><icon name="trash"></icon>Remove</button></div>\
    <button class="theme" @click="toggleTheme" :title="\'Switch to the \' + (isDark ? \'light\' : \'dark\') + \' theme\'"><icon :name="isDark ? \'sun\' : \'moon\'"></icon>{{ isDark ? "Light theme" : "Dark theme" }}</button>\
    <nav><a v-for="m in menu" :key="m.id" :class="{cur: state.page === m.id}" @click="go(m.id)"><icon :name="m.icon"></icon>{{ m.title }}</a></nav>\
  </aside>\
  <main class="main">\
    <div id="msg" :class="state.msg.kind" v-if="state.msg.kind">{{ state.msg.text }}</div>\
    <div v-if="state.serversLoaded && !state.servers.length" class="empty">\
      <h2>Add your server</h2><p>Press “Add server” and enter it as <code>user@address</code>, for example <code>serafim@203.0.113.7</code>.</p></div>\
    <template v-else-if="state.server">\
      <component :is="current.comp" :key="state.server + state.page" v-if="current.comp"></component>\
      <div v-else>\
        <h1>{{ current.title }}</h1>\
        <p class="sub" v-if="!pluginObj">This part is not available on this server.</p>\
        <plugin-panel v-else :plugin="pluginObj" @done="reload"></plugin-panel>\
      </div>\
    </template>\
  </main>\
</div>',
    data: function () { return { state: S }; },
    computed: {
      isDark: function () { return S.theme === 'dark' || (S.theme === 'auto' && window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches); },
      menu: function () { return ALL.filter(function (m) { return !m.plugin || LU.present(m.plugin) || m.plugin === 'backup'; }); },
      current: function () { return ALL.filter(function (m) { return m.id === S.page; })[0] || ALL[0]; },
      pluginObj: function () { var n = this.current.plugin; return S.plugins.filter(function (p) { return p.name === n && p.installed !== false; })[0]; }
    },
    methods: {
      toggleTheme: function () { LU.toggleTheme(); },
      go: function (id) { S.page = id; location.hash = id; LU.clear(); },
      pick: function () { LU.setServer(S.server); LU.clear(); LU.loadPlugins(); },
      reload: function () { LU.loadPlugins(); },
      addServer: function () {
        var v = prompt('Your server as user@address, for example serafim@203.0.113.7');
        if (!v) return;
        LU.api('/api/servers/add', { host: v.trim() }).then(function (r) {
          if (r.ok === false) { LU.say('err', r.error ? r.error.message : 'Could not add the server.'); return; }
          LU.setServer(v.trim());
          LU.loadServers().then(LU.loadPlugins);
        });
      },
      removeServer: function () {
        if (!S.server || !confirm('Remove ' + S.server + ' from your list? Nothing changes on the server.')) return;
        LU.api('/api/servers/remove', { host: S.server }).then(function () {
          LU.setServer(''); LU.loadServers().then(LU.loadPlugins);
        });
      }
    },
    mounted: function () { LU.loadServers().then(LU.loadPlugins); }
  };

  var app = Vue.createApp(Root);
  var C = LU.components;
  app.component('icon', C.Icon);
  app.component('overview', C.Overview);
  app.component('projects', C.Projects);
  app.component('ingress', C.Ingress);
  app.component('shared-secrets', C.SharedSecrets);
  app.component('databases', C.Databases);
  app.component('backups', C.Backups);
  app.component('store-form', C.StoreForm);
  app.component('security', C.Security);
  app.component('server', C.Server);
  app.component('project-card', C.ProjectCard);
  app.component('secrets-panel', C.SecretsPanel);
  app.component('new-project', C.NewProject);
  app.component('plugin-panel', C.PluginPanel);
  app.config.globalProperties.state = S;
  app.config.globalProperties.applyUpdate = LU.applyUpdate;
  app.config.globalProperties.checkUpdate = LU.checkUpdate;
  app.config.globalProperties.applyUiUpdate = LU.applyUiUpdate;
  app.config.globalProperties.loadPlugins = LU.loadPlugins;
  app.mount('#app');
})();
