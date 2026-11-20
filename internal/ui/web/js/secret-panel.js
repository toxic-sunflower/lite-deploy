(function () {
  // The secrets of one project: names and whether each is set. A value is never shown; it is typed here, encrypted on this
  // computer, and the server stores it. The application picks up a new value at the next deploy.
  LU.components.SecretsPanel = {
    props: ['project'],
    data: function () { return { set: null, needed: [], values: {}, newName: '', newValue: '', error: '' }; },
    template: '\
<div class="card">\
  <h2><icon name="shield"></icon> Secrets of {{ project }}</h2>\
  <p class="help">Passwords, tokens and keys of this project. Values are never shown again. After changing one, deploy the project again so that the application uses it.</p>\
  <p v-if="error" class="down">{{ error }}</p>\
  <table v-if="rows.length"><tr><th>Name</th><th>State</th><th>New value</th><th></th></tr>\
    <tr v-for="r in rows" :key="r.name"><td><code>{{ r.name }}</code></td>\
      <td :class="r.isSet ? \'up\' : \'warntxt\'">{{ r.isSet ? "set" + (r.when ? ", " + r.when : "") : "missing" }}</td>\
      <td><input type="password" v-model="values[r.name]" autocomplete="off" placeholder="type a value"></td>\
      <td style="white-space:nowrap"><button class="primary" @click="save(r.name, values[r.name])" :disabled="$root.state.busy">Save</button>\
        <button v-if="r.isSet" @click="remove(r.name)" :disabled="$root.state.busy"><icon name="trash"></icon></button></td></tr></table>\
  <p v-else class="muted">This project has no secrets.</p>\
  <h3>Add a secret</h3>\
  <form class="row" @submit.prevent="save(newName, newValue, true)">\
    <input class="grow" type="text" v-model="newName" placeholder="name: lowercase letters, digits, _" autocomplete="off" spellcheck="false">\
    <input class="grow" type="password" v-model="newValue" placeholder="value" autocomplete="off">\
    <button type="submit" :disabled="$root.state.busy">Add</button></form>\
</div>',
    computed: {
      rows: function () {
        var by = {}, self = this;
        (this.set || []).forEach(function (s) { by[s.name] = s; });
        var names = {};
        this.needed.forEach(function (n) { names[n] = true; });
        Object.keys(by).forEach(function (n) { if (n.charAt(0) !== '_') names[n] = true; });
        return Object.keys(names).sort().map(function (n) {
          var s = by[n];
          return { name: n, isSet: !!s, when: s && s.updated_at ? new Date(s.updated_at).toLocaleString() : '' };
        });
      }
    },
    methods: {
      load: function () {
        var self = this; self.error = '';
        LU.api('/api/secrets?project=' + encodeURIComponent(this.project)).then(function (r) {
          if (r.ok) { self.set = r.data.secrets || []; self.needed = r.data.needed || []; }
          else self.error = r.error ? r.error.message : 'Could not read the secrets.';
        });
      },
      save: function (name, value, isNew) {
        var self = this;
        if (!name || !value) { LU.say('err', 'A name and a value are needed.'); return; }
        LU.run('Saving…', '/api/secret/set', { project: this.project, name: name, value: value }, function () { return 'The secret ' + name + ' is saved. Deploy the project again to use it.'; })
          .then(function (r) { if (r.ok) { self.values[name] = ''; if (isNew) { self.newName = ''; self.newValue = ''; } self.load(); } });
      },
      remove: function (name) {
        var self = this;
        if (!confirm('Remove the secret ' + name + '? The application loses it at the next deploy.')) return;
        LU.run('Removing…', '/api/secret/remove', { project: this.project, name: name }, function () { return 'The secret ' + name + ' was removed.'; }).then(function () { self.load(); });
      }
    },
    mounted: function () { this.load(); }
  };
})();
