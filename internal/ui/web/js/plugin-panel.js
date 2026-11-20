(function () {
  // PluginPanel shows what one plugin offers: its description and commands. The page is built from what the
  // server reports about the plugin, so nothing is hard-coded per plugin.
  function pluginText(d) {
    if (!d) return 'Done.';
    if (Array.isArray(d.packages) && !d.action) return d.packages.length ? d.packages.map(function (x) { return x.name + '  ' + x.version + '  ' + x.installed_at; }).join('\n') : 'No packages have been installed through lite yet.';
    if (d.packages && d.action) return 'Done: ' + d.action + ' ' + d.packages.join(', ');
    if ('installed' in d) return d.name + ': ' + (d.installed ? 'installed, version ' + d.version : 'not installed');
    if (d.output !== undefined) return d.output;
    return JSON.stringify(d, null, 2);
  }

  LU.components.PluginPanel = {
    props: ['plugin'],
    data: function () { return { args: {}, out: {} }; },
    computed: { visible: function () { return (this.plugin.commands || []).filter(function (c) { return !c.advanced; }); } },
    template: '\
<div class="card">\
  <h2>{{ plugin.name }}</h2>\
  <div class="help">{{ plugin.description }}</div>\
  <div v-for="c in visible" :key="c.name" style="margin-top:12px">\
    <div><code>{{ c.name }}</code> <span class="help">{{ c.help }}</span></div>\
    <form class="row" style="margin-top:4px" @submit.prevent="run(c)">\
      <input v-if="c.args" class="grow" type="text" v-model="args[c.name]" :placeholder="c.args" autocomplete="off" spellcheck="false">\
      <button type="submit" :class="{primary: !c.confirm}" :disabled="$root.state.busy">Run</button>\
    </form>\
    <pre class="out" v-if="out[c.name] !== undefined">{{ out[c.name] }}</pre>\
  </div>\
</div>',
    methods: {
      run: function (c) {
        var self = this, raw = (this.args[c.name] || '').trim();
        var list = c.args ? raw.split(/\s+/).filter(Boolean) : [];
        if (c.args && !list.length) { LU.say('err', 'Required: ' + c.args); return; }
        if (c.confirm && !confirm('Run: ' + this.plugin.name + ' ' + c.name + ' ' + list.join(' ') + '?')) return;
        LU.run('Running…', '/api/plugin', { plugin: this.plugin.name, command: c.name, args: list }).then(function (r) {
          if (r.ok) { LU.clear(); self.out[c.name] = pluginText(r.data); self.$emit('done'); }
        });
      }
    }
  };
})();
