#!/usr/bin/env python3
"""Шаблон отчёта LedgerFox.

Скопируйте этот файл в scripts/reports/<имя>_report.py — новый отчёт
автоматически появится в меню `make report`. Префикс `_` оставляет файл
служебным: `_template.py` в меню не попадает, ваша копия (без `_`) — попадёт.

Требования к отчёту (см. docs/development.md, раздел «Python-отчёты»):
  - первая строка этого docstring — заголовок пункта меню;
  - только стандартная библиотека Python 3.10+;
  - база открывается только для чтения; личные данные не логируются;
  - ошибки (нет базы/таблиц) — понятное сообщение в stderr и код 1.
"""

from __future__ import annotations

import argparse
import sqlite3
import sys
from pathlib import Path

DEFAULT_DB = "lkdr.db"

# Таблицы, без которых отчёт не имеет смысла; проверьте их наличие.
REQUIRED_TABLES = ("receipts", "fiscal_data")


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="TODO: описание отчёта для --help.")
    parser.add_argument("--db", default=DEFAULT_DB, type=Path, help="Путь к lkdr.db")
    parser.add_argument("--top", default=10, type=int, help="Строк в топах")
    return parser.parse_args(argv)


def open_database(path: Path) -> sqlite3.Connection:
    """Открывает базу только для чтения и проверяет обязательные таблицы."""
    if not path.exists():
        raise FileNotFoundError(f"База данных не найдена: {path}")

    connection = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    try:
        missing = [
            table
            for table in REQUIRED_TABLES
            if connection.execute(
                "select 1 from sqlite_master where type = 'table' and name = ?", (table,)
            ).fetchone()
            is None
        ]
        if missing:
            raise LookupError(f"В базе нет таблиц: {', '.join(missing)}")
    except Exception:
        connection.close()
        raise

    return connection


def build_report(connection: sqlite3.Connection, args: argparse.Namespace) -> str:
    # TODO: основная логика отчёта. Возвращайте готовый текст — так проще
    # тестировать (см. scripts/tests/test_reports.py).
    (receipts,) = connection.execute("select count(*) from receipts").fetchone()
    return f"Чеков в базе: {receipts}"


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)

    try:
        connection = open_database(args.db)
    except (FileNotFoundError, LookupError) as err:
        print(err, file=sys.stderr)
        return 1

    try:
        print(build_report(connection, args))
    finally:
        connection.close()

    return 0


if __name__ == "__main__":
    sys.exit(main())
