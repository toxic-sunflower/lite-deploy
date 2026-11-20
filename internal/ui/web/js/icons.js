(function () {
  // Small outline icons drawn here, so the page needs nothing from the internet.
  var P = {
    home: 'M5 12H3l9-9l9 9h-2 M5 12v7a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-7 M9 21v-6a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2v6',
    apps: 'M4 4h6v6H4z M14 4h6v6h-6z M4 14h6v6H4z M14 14h6v6h-6z',
    world: 'M3 12a9 9 0 1 0 18 0a9 9 0 1 0-18 0 M3.6 9h16.8 M3.6 15h16.8 M11.5 3a17 17 0 0 0 0 18 M12.5 3a17 17 0 0 1 0 18',
    database: 'M4 6a8 3 0 1 0 16 0a8 3 0 1 0-16 0 M4 6v6a8 3 0 0 0 16 0V6 M4 12v6a8 3 0 0 0 16 0v-6',
    backup: 'M7 18a4.5 4.5 0 0 1 0-9a5 4.5 0 0 1 11 2h1a3.5 3.5 0 0 1 0 7h-1 M9 15l3-3l3 3 M12 12v9',
    shield: 'M12 3a12 12 0 0 0 8.5 3a12 12 0 0 1-8.5 15a12 12 0 0 1-8.5-15A12 12 0 0 0 12 3 M9 12l2 2l4-4',
    settings: 'M4 6h16 M4 12h16 M4 18h16 M8 4v4 M16 10v4 M8 16v4',
    server: 'M3 4h18v6H3z M3 14h18v6H3z M7 7v.01 M7 17v.01',
    plus: 'M12 5v14 M5 12h14',
    refresh: 'M20 11a8 8 0 1 0-2 5.5 M20 4v7h-7',
    alert: 'M12 3l10 18H2z M12 10v4 M12 17v.01',
    info: 'M3 12a9 9 0 1 0 18 0a9 9 0 1 0-18 0 M12 8v.01 M11 12h1v4h1',
    check: 'M5 12l5 5l10-10',
    sun: 'M12 8a4 4 0 1 0 0 8a4 4 0 0 0 0-8 M12 2v2 M12 20v2 M4.9 4.9l1.4 1.4 M17.7 17.7l1.4 1.4 M2 12h2 M20 12h2 M4.9 19.1l1.4-1.4 M17.7 6.3l1.4-1.4',
    moon: 'M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z',
    trash: 'M4 7h16 M10 11v6 M14 11v6 M5 7l1 12a2 2 0 0 0 2 2h8a2 2 0 0 0 2-2l1-12 M9 7V4h6v3'
  };
  LU.components.Icon = {
    props: ['name'],
    template: '<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="d"/></svg>',
    computed: { d: function () { return P[this.name] || P.info; } }
  };
})();
