#!/usr/bin/env python3
"""Builds the documentation site from the Markdown files of the repository into the _site directory.

Run it from the repository root: python3 site/build.py (the markdown package is required).
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

# (file, page address, menu title, description in the document list)
GROUPS = [
    ("Start", [
        ("README.md", "index", "Install", "What it is and how to install it."),
    ]),
    ("Tutorials", [
        ("docs/quickstart.md", "quickstart", "Quickstart", "From a bare server to a running site in one sitting."),
        ("docs/first-project.md", "first-project", "Your first project", "A container project: description, secrets, create, deploy, roll back."),
        ("docs/publish-a-site.md", "publish-a-site", "Publish a site", "Serve a site with Caddy's own TLS certificate or through a Cloudflare Tunnel, and switch between them."),
        ("docs/process-project.md", "process-project", "A project without containers", "Deploy a binary, Node or compose app from a signed archive."),
        ("docs/ci.md", "ci", "Deploy from CI", "A restricted key, the gatekeeper and a GitHub Actions workflow."),
        ("docs/remote-database.md", "remote-database", "A database on another server", "Connect a project to PostgreSQL on a second server."),
        ("docs/backups.md", "backups", "Backups and restore", "Encrypted backups, storage outside the server and restoring a lost server."),
        ("docs/protect-the-server.md", "protect-the-server", "Protect the server", "SSH, fail2ban, updates, the firewall and `lite verify`."),
        ("docs/ai-assistants.md", "ai-assistants", "Use an AI assistant", "Let an AI assistant read your servers and deploy, with your approval."),
        ("docs/server-config.md", "server-config", "Describe the server in a file", "Save a server as a file, or write one, and set up a new server with it."),
        ("docs/maintenance.md", "maintenance", "Update and troubleshoot", "Updating, uninstalling and what to do when something fails."),
    ]),
]
PAGES = {f: (slug, title) for _, items in GROUPS for f, slug, title, _ in items}

CODE_MD = re.compile(r"<code>([A-Za-z0-9_.-]+\.md)</code>")


BASENAMES = {Path(f).name: f for f in PAGES}


def link_md(body: str) -> str:
    def sub(m):
        name = m.group(1)
        if name in PAGES:
            slug = PAGES[name][0]
            return f'<a href="{"./" if slug == "index" else slug + ".html"}"><code>{name}</code></a>'
        return m.group(0)
    body = CODE_MD.sub(sub, body)

    def href(m):
        name = m.group(1)
        name = BASENAMES.get(name, name)
        if name in PAGES:
            slug = PAGES[name][0]
            return f'href="{"./" if slug == "index" else slug + ".html"}"'
        return f'href="{REPO}/blob/main/{name}"'
    # plain links to repository documents: a site page if there is one, otherwise the file on GitHub
    return re.sub(r'href="([A-Za-z0-9_./-]+\.md)"', href, body)


def wrap_tables(body: str) -> str:
    return body.replace("<table>", '<div class="tw"><table>').replace("</table>", "</table></div>")


def render(text: str) -> str:
    md = markdown.Markdown(extensions=["tables", "fenced_code", "toc", "sane_lists"],
                           extension_configs={"toc": {"permalink": False}})
    return wrap_tables(link_md(md.convert(text)))


def nav(cur: str) -> str:
    parts = ['<a class="brand" href="./">lite-deploy</a>']
    for group, items in GROUPS:
        parts.append(f"<h4>{html.escape(group)}</h4>")
        for _, slug, title, _ in items:
            href = "./" if slug == "index" else f"{slug}.html"
            c = ' class="cur"' if slug == cur else ""
            parts.append(f'<a{c} href="{href}">{html.escape(title)}</a>')
    parts.append("<h4>Links</h4>")
    parts.append(f'<a href="{REPO}">Source code</a>')
    parts.append(f'<a href="{REPO}/releases">Releases</a>')
    parts.append('<a href="install.sh">install.sh (server)</a>')
    parts.append('<a href="install-ui.sh">install-ui.sh (your computer)</a>')
    return "\n".join(parts)


def page(slug: str, title: str, body: str, src: str | None) -> str:
    foot = f'<p class="src"><a href="{REPO}/blob/main/{src}">Source file on GitHub</a></p>' if src else ""
    full_title = "lite-deploy" if slug == "index" else f"{title} · lite-deploy"
    return f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{html.escape(full_title)}</title>
<meta name="description" content="Set up a Linux server and deploy projects to it, with no root needed for daily work.">
<link rel="stylesheet" href="style.css">
</head>
<body>
<div class="layout">
<nav class="side">
{nav(slug)}
</nav>
<main>
{body}
{foot}
</main>
</div>
</body>
</html>
"""


def main() -> int:
    if OUT.exists():
        shutil.rmtree(OUT)
    OUT.mkdir()
    for f in ("style.css", "install.sh", "liteup", "install-ui.sh", "install-ui.ps1", "CNAME"):
        shutil.copy(ROOT / "site" / f, OUT / f)
    # the old addresses of the interface installer keep working
    shutil.copy(ROOT / "site" / "install-ui.sh", OUT / "lite-ui")
    shutil.copy(ROOT / "site" / "install-ui.ps1", OUT / "lite-ui.ps1")

    for _, items in GROUPS:
        for fname, slug, title, _ in items:
            src = ROOT / fname
            if not src.exists():
                print(f"no file {fname}", file=sys.stderr)
                return 1
            body = render(src.read_text(encoding="utf-8"))
            if slug == "index":
                rows = []
                for _, its in GROUPS[1:]:
                    for f2, s2, t2, d2 in its:
                        rows.append(f'<li><a href="{s2}.html">{html.escape(t2)}</a><span>{html.escape(d2)}</span></li>')
                body += '\n<h2 id="documentation">Documentation</h2>\n<ul class="docs">\n' + "\n".join(rows) + "\n</ul>"
            (OUT / f"{slug}.html").write_text(page(slug, title, body, fname), encoding="utf-8")
    print(f"pages built: {sum(len(i) for _, i in GROUPS)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
