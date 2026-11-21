(function () {
  // The secrets of the whole server: those that several projects use (for example one registry token for all of them).
  // The secrets of one project are on its card in Projects.
  LU.components.SharedSecrets = {
    template: '\
<div>\
  <h1>Secrets <tip text="Secrets are passwords and keys. After saving, their value is never shown again. A secret shared by several projects is written as shared:NAME."></tip></h1>\
  <p class="sub">Secrets of the whole server, shared by several projects. A project uses one by writing <code>shared:NAME</code>. The secrets of a single project are on its card in Projects.</p>\
  <secrets-panel project="shared"></secrets-panel>\
</div>'
  };
})();
