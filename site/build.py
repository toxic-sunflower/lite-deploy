#!/usr/bin/env python3
"""Builds the documentation site from the Markdown files of the repository into the _site directory.

Run it from the repository root: python3 site/build.py (the markdown package is required).
The pictures in site/img are taken by site/screenshots/make.sh from the real interface.
"""
import html
import re
import shutil
import sys
from pathlib import Path

import markdown

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "_site"
REPO = "https://github.com/toxic-sunflower/lite-deploy"
DESCRIPTION = "Set up a Linux server and deploy projects to it, with no root needed for daily work."

# (file, page address, menu title, description in the document list)
START = [("README.md", "reference", "Install and reference", "Installing, every command and every plugin.")]
TUTORIALS = [
    ("docs/quickstart.md", "quickstart", "Quickstart", "From a bare server to a running site in one sitting."),
    ("docs/first-project.md", "first-project", "Your first project", "A container project: description, secrets, create, deploy, roll back."),
    ("docs/publish-a-site.md", "publish-a-site", "Publish a site", "Serve a site with Caddy's own TLS certificate or through a Cloudflare Tunnel, and switch between them."),
    ("docs/process-project.md", "process-project", "A project without containers", "Deploy a binary, Node or compose app from a signed archive."),
    ("docs/ci.md", "ci", "Deploy from CI", "A restricted key, the gatekeeper and a GitHub Actions workflow."),
    ("docs/remote-database.md", "remote-database", "A database on another server", "Connect a project to PostgreSQL on a second server."),
    ("docs/backups.md", "backups", "Backups and restore", "Encrypted backups per project, copies outside the server and restoring a lost server."),
    ("docs/protect-the-server.md", "protect-the-server", "Protect the server", "What protection does, the firewall, confirming changes with your key."),
    ("docs/server-config.md", "server-config", "Describe the server in a file", "Save a server as a file, or write one, and set up a new server with it."),
    ("docs/ai-assistants.md", "ai-assistants", "Use an AI assistant", "Let an AI assistant read your servers and deploy, with your approval."),
    ("docs/maintenance.md", "maintenance", "Update and troubleshoot", "Updating, uninstalling and what to do when something fails."),
]
GROUPS = [("Start", START), ("Tutorials", TUTORIALS)]
PAGES = {f: (slug, title) for _, items in GROUPS for f, slug, title, _ in items}
BASENAMES = {Path(f).name: f for f in PAGES}

# small outline icons (the same set as the interface)
ICONS = {
    "home": "M5 12H3l9-9l9 9h-2 M5 12v7a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-7 M9 21v-6a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2v6",
    "apps": "M4 4h6v6H4z M14 4h6v6h-6z M4 14h6v6H4z M14 14h6v6h-6z",
    "world": "M3 12a9 9 0 1 0 18 0a9 9 0 1 0-18 0 M3.6 9h16.8 M3.6 15h16.8 M11.5 3a17 17 0 0 0 0 18 M12.5 3a17 17 0 0 1 0 18",
    "database": "M4 6a8 3 0 1 0 16 0a8 3 0 1 0-16 0 M4 6v6a8 3 0 0 0 16 0V6 M4 12v6a8 3 0 0 0 16 0v-6",
    "backup": "M7 18a4.5 4.5 0 0 1 0-9a5 4.5 0 0 1 11 2h1a3.5 3.5 0 0 1 0 7h-1 M9 15l3-3l3 3 M12 12v9",
    "shield": "M12 3a12 12 0 0 0 8.5 3a12 12 0 0 1-8.5 15a12 12 0 0 1-8.5-15A12 12 0 0 0 12 3 M9 12l2 2l4-4",
    "settings": "M4 6h16 M4 12h16 M4 18h16 M8 4v4 M16 10v4 M8 16v4",
    "server": "M3 4h18v6H3z M3 14h18v6H3z M7 7v.01 M7 17v.01",
    "download": "M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2 M7 11l5 5l5-5 M12 4v12",
    "bolt": "M13 3l-9 12h7l-1 6l9-12h-7z",
    "terminal": "M4 17l6-6l-6-6 M12 19h8",
    "refresh": "M20 11a8 8 0 1 0-2 5.5 M20 4v7h-7",
    "sparkles": "M12 3l2 5l5 2l-5 2l-2 5l-2-5l-5-2l5-2z M19 15l.8 2.2L22 18l-2.2.8L19 21l-.8-2.2L16 18l2.2-.8z",
    "tool": "M14 6l4 4 M3 21l9-9 M15 3a4 4 0 0 0 5 5l-9 9a2.8 2.8 0 0 1-4-4z",
    "arrow": "M5 12h14 M13 6l6 6l-6 6",
    "github": "M9 19c-4.3 1.4-4.3-2.5-6-3 M15 21v-3.9a3.4 3.4 0 0 0-1-2.6c3.3-.4 6.8-1.6 6.8-7.3a5.6 5.6 0 0 0-1.6-3.9a5.2 5.2 0 0 0-.1-3.9s-1.3-.4-4.1 1.5a14 14 0 0 0-7.4 0C5.8 1.9 4.5 2.3 4.5 2.3a5.2 5.2 0 0 0-.1 3.9A5.6 5.6 0 0 0 2.8 10c0 5.7 3.5 6.9 6.8 7.3a3.4 3.4 0 0 0-1 2.6V21",
    "moon": "M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z",
    "sun": "M12 8a4 4 0 1 0 0 8a4 4 0 0 0 0-8 M12 2v2 M12 20v2 M4.9 4.9l1.4 1.4 M17.7 17.7l1.4 1.4 M2 12h2 M20 12h2 M4.9 19.1l1.4-1.4 M17.7 6.3l1.4-1.4",
    "menu": "M4 6h16 M4 12h16 M4 18h16",
    "check": "M5 12l5 5l10-10",
    "lock": "M5 11h14v10H5z M8 11V7a4 4 0 0 1 8 0v4",
}
NAV_ICONS = {"index": "home", "reference": "download", "quickstart": "bolt", "first-project": "apps", "publish-a-site": "world",
             "process-project": "terminal", "ci": "refresh", "remote-database": "database", "backups": "backup",
             "protect-the-server": "shield", "server-config": "settings", "ai-assistants": "sparkles", "maintenance": "tool"}


def icon(name: str) -> str:
    return ('<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" '
            f'stroke-linejoin="round" aria-hidden="true"><path d="{ICONS[name]}"/></svg>')


CODE_MD = re.compile(r"<code>([A-Za-z0-9_.-]+\.md)</code>")


def target(name: str) -> str:
    slug = PAGES[name][0]
    return f"{slug}.html"


def link_md(body: str) -> str:
    def sub(m):
        name = m.group(1)
        if name in PAGES:
            return f'<a href="{target(name)}"><code>{name}</code></a>'
        return m.group(0)
    body = CODE_MD.sub(sub, body)

    def href(m):
        name = BASENAMES.get(m.group(1), m.group(1))
        if name in PAGES:
            return f'href="{target(name)}"'
        return f'href="{REPO}/blob/main/{name}"'
    # plain links to repository documents: a site page if there is one, otherwise the file on GitHub
    return re.sub(r'href="([A-Za-z0-9_./-]+\.md)"', href, body)


def wrap_tables(body: str) -> str:
    return body.replace("<table>", '<div class="tw"><table>').replace("</table>", "</table></div>")


def wrap_code(body: str) -> str:
    return re.sub(r"<pre>(.*?)</pre>", lambda m: f'<div class="pre"><pre>{m.group(1)}</pre><button class="copy" type="button">Copy</button></div>', body, flags=re.S)


def figure(name: str, alt: str) -> str:
    a = html.escape(alt)
    return (f'<figure class="shot"><div class="frame"><img class="only-light" src="img/{name}-light.png" alt="{a}" loading="lazy">'
            f'<img class="only-dark" src="img/{name}-dark.png" alt="{a}" loading="lazy"></div><figcaption>{a}</figcaption></figure>')


def pictures(body: str) -> str:
    # ![caption](img/name.png) in a document becomes a picture that follows the theme
    return re.sub(r'<p><img alt="([^"]*)" src="img/([a-z0-9-]+)\.png"\s*/?></p>', lambda m: figure(m.group(2), m.group(1)), body)


def render(text: str) -> str:
    md = markdown.Markdown(extensions=["tables", "fenced_code", "toc", "sane_lists"], extension_configs={"toc": {"permalink": False}})
    return wrap_code(pictures(wrap_tables(link_md(md.convert(text)))))


def nav(cur: str) -> str:
    parts = [f'<a class="brand" href="./"><span class="logo">{icon("server")}</span>lite-deploy</a>',
             f'<button class="theme" type="button" id="theme">{icon("moon")}<span>Theme</span></button>']
    items = [("index", "Home", "./")]
    parts.append('<h4>Start</h4>')
    parts.append(f'<a class="item{" cur" if cur == "index" else ""}" href="./">{icon("home")}Home</a>')
    for group, its in GROUPS:
        if group == "Tutorials":
            parts.append(f"<h4>{html.escape(group)}</h4>")
        for _, slug, title, _ in its:
            c = " cur" if slug == cur else ""
            parts.append(f'<a class="item{c}" href="{slug}.html">{icon(NAV_ICONS[slug])}{html.escape(title)}</a>')
    parts.append("<h4>Links</h4>")
    parts.append(f'<a class="item" href="{REPO}">{icon("github")}Source code</a>')
    parts.append(f'<a class="item" href="{REPO}/releases">{icon("download")}Releases</a>')
    parts.append(f'<a class="item" href="install.sh">{icon("terminal")}install.sh (server)</a>')
    parts.append(f'<a class="item" href="install-ui.sh">{icon("terminal")}install-ui.sh (workstation)</a>')
    return "\n".join(parts)


SCRIPT = """
(function () {
  var root = document.documentElement, KEY = 'lite-site-theme';
  var btn = document.getElementById('theme');
  function dark() { var t = root.getAttribute('data-theme'); return t === 'dark' || (!t && matchMedia('(prefers-color-scheme: dark)').matches); }
  function label() { if (!btn) return; btn.querySelector('span').textContent = dark() ? 'Light theme' : 'Dark theme'; }
  if (btn) btn.addEventListener('click', function () {
    var t = dark() ? 'light' : 'dark'; root.setAttribute('data-theme', t);
    try { localStorage.setItem(KEY, t); } catch (e) {}
    label();
  });
  label();
  document.querySelectorAll('.copy').forEach(function (b) {
    b.addEventListener('click', function () {
      var code = b.parentNode.querySelector('pre').innerText.replace(/\\n$/, '');
      var done = function () { b.textContent = 'Copied'; setTimeout(function () { b.textContent = 'Copy'; }, 1500); };
      if (navigator.clipboard) navigator.clipboard.writeText(code).then(done); else done();
    });
  });
  var menu = document.getElementById('menu');
  if (menu) menu.addEventListener('click', function () { document.body.classList.toggle('open'); });
})();
"""

HEAD_THEME = "(function(){try{var t=localStorage.getItem('lite-site-theme');if(t==='light'||t==='dark')document.documentElement.setAttribute('data-theme',t);}catch(e){}})();"

FAVICON = ("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'%3E%3Crect width='32' height='32' rx='8' fill='%231a1a1a'/%3E"
           "%3Cpath d='M7 10h18v5H7zM7 18h18v5H7z' fill='none' stroke='%23fff' stroke-width='2'/%3E%3C/svg%3E")


def page(slug: str, title: str, body: str, src: str | None, crumb: str = "", extra: str = "") -> str:
    foot = f'<p class="src"><a href="{REPO}/blob/main/{src}">Edit this page on GitHub</a></p>' if src else ""
    full_title = "lite-deploy" if slug == "index" else f"{title} · lite-deploy"
    cr = f'<div class="crumb">{html.escape(crumb)}</div>' if crumb else ""
    return f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{html.escape(full_title)}</title>
<meta name="description" content="{html.escape(DESCRIPTION)}">
<meta property="og:title" content="{html.escape(full_title)}">
<meta property="og:description" content="{html.escape(DESCRIPTION)}">
<meta property="og:image" content="https://lite-deploy.prozorov.me/img/overview-light.png">
<link rel="icon" href="{FAVICON}">
<link rel="stylesheet" href="style.css">
<script>{HEAD_THEME}</script>
</head>
<body>
<button class="menu-btn" id="menu" type="button">{icon("menu")} lite-deploy</button>
<div class="layout">
<nav class="side">
{nav(slug)}
</nav>
<main>
{cr}
{body}
{extra}
{foot}
</main>
</div>
<script>{SCRIPT}</script>
</body>
</html>
"""


def home() -> str:
    def card(ic, title, text):
        return f'<div class="feature">{icon(ic)}<b>{title}</b><span>{text}</span></div>'

    def row(title, text, img, alt, flip=False, link=None):
        more = f' <a href="{link[0]}">{link[1]} →</a>' if link else ""
        return (f'<div class="row2{" flip" if flip else ""}"><div><h3>{title}</h3><p>{text}{more}</p></div>{figure(img, alt)}</div>')

    docs = []
    for f, slug, title, desc in TUTORIALS:
        docs.append(f'<li><a href="{slug}.html"><b>{html.escape(title)}</b><span>{html.escape(desc)}</span></a></li>')
    return f"""
<section class="hero">
<h1>Your server, set up and deployed to, without the terminal.</h1>
<p class="big">lite-deploy sets up a Linux server, deploys your projects to it with no downtime, publishes them, backs them up and keeps them
protected. You work in a clear web page on your workstation; the terminal stays there for whoever likes it.</p>
<div class="btns"><a class="btn primary" href="quickstart.html">{icon("bolt")}Quickstart</a><a class="btn" href="#install">{icon("download")}Install</a>
<a class="btn" href="{REPO}">{icon("github")}GitHub</a></div>
{figure("overview", "The Overview page: the state of the server in plain words, what needs attention, your projects.")}
</section>

<h2 id="how">How it works</h2>
<div class="steps">
<div class="step"><b>Install on the server</b><span>Two commands. The server is protected by default: SSH by key only, protection against password guessing, automatic security updates.</span></div>
<div class="step"><b>Open the page on your workstation</b><span>A small program, <code>lite-ui</code>, reaches the server over SSH with your key. Nothing of it runs on the server, and nothing is opened to the internet.</span></div>
<div class="step"><b>Create a project and deploy</b><span>Fill in a form, press deploy. The new version takes traffic only when it answers its health check; a rollback is one click.</span></div>
</div>

<h2 id="why">What you get</h2>
<div class="features">
{card("shield", "Safe by default", "No root for daily work. Dangerous changes can be confirmed with a key that only your workstation holds.")}
{card("refresh", "Deploys without downtime", "Blue and green copies side by side. Deploy from the page or from CI with a key that can do nothing else.")}
{card("world", "Publish your way", "Caddy gets the TLS certificate itself, or visitors come through a Cloudflare Tunnel. Switch in one click if one of them fails.")}
{card("backup", "Backups you control", "Per project: settings, secrets, database, files. Encrypted with a key you keep, copied to Backblaze, S3 or another server.")}
{card("database", "Databases and secrets", "PostgreSQL on the server or on another one. Secrets are encrypted on your workstation and never shown again; share one between projects.")}
{card("settings", "A server as a file", "Save a server as a small file, or write one, and set up the next server exactly the same way.")}
{card("sparkles", "Works with AI assistants", "Let Claude read your servers and deploy with your approval, through a ready extension for Claude Desktop.")}
{card("server", "Made for one server", "Not a platform to run. A few programs you already know on one Linux machine, looked after for you.")}
</div>

{row("Projects: deploy and roll back", "Every project shows which copy is live, whether each part answers, and its history. Paste a commit and press Deploy; if something is wrong, Roll back.", "projects", "A project card with the slots, the deploy form and the history.", False, ("first-project.html", "Your first project"))}
{row("Ingress: your domains", "See every domain of the server and how visitors reach it. Switch a site between the Cloudflare Tunnel and direct publishing when you need to.", "ingress", "The Ingress page: domains, the way each is published, the Cloudflare Tunnel.", True, ("publish-a-site.html", "Publish a site"))}
{row("Backups, per project", "Choose for the server and for each project what is copied, where and when. The key is made on your workstation and never leaves it.", "backups", "The Backups page with a target for the server and for each project.", False, ("backups.html", "Backups and restore"))}
{row("Protection you can read", "The checks of the server in plain words, the open ports, and a switch that makes dangerous changes work only from your workstation.", "protection", "The Protection page: checks, the key confirmation and the open ports.", True, ("protect-the-server.html", "Protect the server"))}

<h2 id="install">Install</h2>
<p>On a Linux server with systemd (Ubuntu or Debian recommended), as an administrator with <code>sudo</code>:</p>
<div class="pre install"><pre><code>curl -fsSL https://lite-deploy.prozorov.me/install.sh | sudo sh
sudo liteup install
sudo lite init --user YOUR_NAME</code></pre><button class="copy" type="button">Copy</button></div>
<p>Then, on your workstation (Windows PowerShell: <code>irm https://lite-deploy.prozorov.me/install-ui.ps1 | iex</code>):</p>
<div class="pre"><pre><code>curl -fsSL https://lite-deploy.prozorov.me/install-ui.sh | sh
lite-ui</code></pre><button class="copy" type="button">Copy</button></div>
<p>The first time, <code>lite init</code> asks for your public SSH key; it is the only command that needs root. Details are in the <a href="quickstart.html">Quickstart</a> and in <a href="reference.html">Install and reference</a>.</p>

<h2 id="documentation">Documentation</h2>
<ul class="docs">
{chr(10).join(docs)}
</ul>
"""


def neighbours(slug: str):
    order = [s for _, s, _, _ in TUTORIALS]
    if slug not in order:
        return ""
    i = order.index(slug)
    titles = {s: t for _, s, t, _ in TUTORIALS}
    prev = f'<a class="prev" href="{order[i-1]}.html"><small>Previous</small><span>{html.escape(titles[order[i-1]])}</span></a>' if i > 0 else "<div></div>"
    nxt = f'<a class="next" href="{order[i+1]}.html"><small>Next</small><span>{html.escape(titles[order[i+1]])}</span></a>' if i + 1 < len(order) else "<div></div>"
    return f'<div class="pn">{prev}{nxt}</div>'


def main() -> int:
    if OUT.exists():
        shutil.rmtree(OUT)
    OUT.mkdir()
    for f in ("style.css", "install.sh", "liteup", "install-ui.sh", "install-ui.ps1", "CNAME"):
        shutil.copy(ROOT / "site" / f, OUT / f)
    shutil.copytree(ROOT / "site" / "img", OUT / "img")
    # the old addresses of the interface installer keep working
    shutil.copy(ROOT / "site" / "install-ui.sh", OUT / "lite-ui")
    shutil.copy(ROOT / "site" / "install-ui.ps1", OUT / "lite-ui.ps1")

    (OUT / "index.html").write_text(page("index", "Home", home(), None), encoding="utf-8")
    for group, items in GROUPS:
        for fname, slug, title, _ in items:
            src = ROOT / fname
            if not src.exists():
                print(f"no file {fname}", file=sys.stderr)
                return 1
            body = render(src.read_text(encoding="utf-8"))
            crumb = "Reference" if group == "Start" else "Tutorial"
            (OUT / f"{slug}.html").write_text(page(slug, title, body, fname, crumb, neighbours(slug)), encoding="utf-8")
    print(f"pages built: {1 + sum(len(i) for _, i in GROUPS)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
