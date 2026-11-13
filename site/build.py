#!/usr/bin/env python3
"""Собирает сайт документации из Markdown-файлов репозитория в каталог _site.

Запуск из корня репозитория: python3 site/build.py (нужен пакет markdown).
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

# (файл, адрес страницы, название в меню, описание в списке документов)
GROUPS = [
    ("Начало", [
        ("README.md", "index", "Установка", "Что это и как поставить."),
    ]),
    ("Как это устроено", [
        ("VISION.md", "vision", "Замысел", "Зачем проект, принципы и границы."),
        ("ARCHITECTURE.md", "architecture", "Архитектура", "Части системы, безопасность, модель угроз."),
        ("DEPLOY-PATTERN.md", "deploy-pattern", "Схема деплоя", "Ограниченный ключ, привратник, blue-green, чек-лист изоляции проектов."),
        ("SPEC-v0.md", "spec-v0", "Спецификация v0", "Схемы, план, действия исполнителя, команды CLI."),
    ]),
    ("Проект", [
        ("DECISIONS.md", "decisions", "Решения", "Журнал принятых решений с причинами."),
        ("PLAN.md", "plan", "План", "Очередь работ и состояние."),
        ("COMPARISON-PLAN.md", "comparison", "Сравнение с аналогами", "Coolify, Dokploy, Kamal и другие."),
        ("WSL-TEST.md", "wsl-test", "Проверка в WSL", "Как проверить систему на тестовом сервере."),
        ("RELEASING.md", "releasing", "Выпуск релиза", "Как выпускаются и подписываются релизы."),
    ]),
]
PAGES = {f: (slug, title) for _, items in GROUPS for f, slug, title, _ in items}

CODE_MD = re.compile(r"<code>([A-Za-z0-9_.-]+\.md)</code>")


def link_md(body: str) -> str:
    def sub(m):
        name = m.group(1)
        if name in PAGES:
            slug = PAGES[name][0]
            return f'<a href="{"./" if slug == "index" else slug + ".html"}"><code>{name}</code></a>'
        return m.group(0)
    return CODE_MD.sub(sub, body)


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
    parts.append("<h4>Ссылки</h4>")
    parts.append(f'<a href="{REPO}">Исходный код</a>')
    parts.append(f'<a href="{REPO}/releases">Релизы</a>')
    parts.append('<a href="install.sh">install.sh</a>')
    return "\n".join(parts)


def page(slug: str, title: str, body: str, src: str | None) -> str:
    foot = f'<p class="src"><a href="{REPO}/blob/main/{src}">Исходный файл на GitHub</a></p>' if src else ""
    full_title = "lite-deploy" if slug == "index" else f"{title} · lite-deploy"
    return f"""<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{html.escape(full_title)}</title>
<meta name="description" content="Лёгкая система настройки сервера и деплоя для одного VDS с несколькими проектами.">
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
    for f in ("style.css", "install.sh", "CNAME"):
        shutil.copy(ROOT / "site" / f, OUT / f)

    for _, items in GROUPS:
        for fname, slug, title, _ in items:
            src = ROOT / fname
            if not src.exists():
                print(f"нет файла {fname}", file=sys.stderr)
                return 1
            body = render(src.read_text(encoding="utf-8"))
            if slug == "index":
                rows = []
                for _, its in GROUPS[1:]:
                    for f2, s2, t2, d2 in its:
                        rows.append(f'<li><a href="{s2}.html">{html.escape(t2)}</a><span>{html.escape(d2)}</span></li>')
                body += '\n<h2 id="dokumentaciya">Документация</h2>\n<ul class="docs">\n' + "\n".join(rows) + "\n</ul>"
            (OUT / f"{slug}.html").write_text(page(slug, title, body, fname), encoding="utf-8")
    print(f"собрано страниц: {sum(len(i) for _, i in GROUPS)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
