#!/usr/bin/env python3
"""Пример отчёта: краткая сводка по базе чеков LKDR."""

from __future__ import annotations

import argparse
import sqlite3
import sys
from pathlib import Path

DEFAULT_DB = "lkdr.db"
REQUIRED_TABLES = ("receipts",)


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Краткая сводка по базе LKDR: чеки, период, топ магазинов.")
    parser.add_argument("--db", default=DEFAULT_DB, type=Path, help="Путь к lkdr.db")
    parser.add_argument("--top", default=5, type=int, help="Сколько магазинов в топе")
    return parser.parse_args(argv)


def open_database(path: Path) -> sqlite3.Connection:
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
    lines: list[str] = [f"База: {args.db}"]

    (receipts,) = connection.execute("select count(*) from receipts").fetchone()
    (total,) = connection.execute(
        "select coalesce(sum(cast(total_sum as real)), 0) from receipts"
    ).fetchone()
    first, last = connection.execute(
        "select min(receive_date), max(receive_date) from receipts"
    ).fetchone()
    lines.append(f"Чеков: {receipts}")
    lines.append(f"Сумма по чекам: {total:.2f} руб.")
    if first is not None:
        lines.append(f"Период: {first} — {last}")

    if receipts and args.top > 0:
        # total_sum в receipts хранится строкой — приводим к числу при агрегации.
        top = connection.execute(
            """
            select kkt_owner, count(*), sum(cast(total_sum as real))
            from receipts
            group by kkt_owner
            order by 3 desc
            limit ?
            """,
            (args.top,),
        ).fetchall()
        lines.append("")
        lines.append(f"Топ-{len(top)} магазинов:")
        for store, count, total in top:
            lines.append(f"  {store}: {count} чеков, {total:.2f} руб.")

    return "\n".join(lines)


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
