#!/usr/bin/env python3
"""Интерактивное меню Python-отчётов LedgerFox.

Сканирует scripts/reports/*.py и показывает нумерованное меню; выбранный
отчёт запускается как отдельный процесс, его код возврата пробрасывается.

Правила каталога reports (см. docs/development.md, раздел «Python-отчёты»):
  - каждый *.py — отдельный отчёт, id = имя файла без расширения;
  - файлы с префиксом `_` служебные и в меню не попадают;
  - заголовок пункта меню — первая строка docstring скрипта.

Запуск:
    ./scripts/report.py                    # меню (номер или id, q — выход)
    ./scripts/report.py <id> [аргументы]   # без меню — для скриптов и cron
    ./scripts/report.py --list             # список отчётов: id<TAB>заголовок
"""

from __future__ import annotations

import ast
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

REPORTS_DIR = Path(__file__).resolve().parent / "reports"


@dataclass(frozen=True)
class Report:
    id: str
    title: str
    path: Path


def module_title(path: Path) -> str:
    """Первая строка docstring модуля; без docstring — заглушка.

    Docstring читается через ast, без импорта: файлы отчётов не выполняются
    до явного выбора в меню.
    """
    try:
        tree = ast.parse(path.read_text(encoding="utf-8"))
    except (OSError, SyntaxError):
        return "(не удалось прочитать описание)"

    doc = ast.get_docstring(tree) or ""
    first_line = doc.strip().splitlines()[0].strip() if doc.strip() else ""
    return first_line or "(без описания)"


def discover_reports(reports_dir: Path = REPORTS_DIR) -> list[Report]:
    """Все отчёты каталога: *.py без префикса `_`, по алфавиту имени файла."""
    if not reports_dir.is_dir():
        return []

    return [
        Report(id=path.stem, title=module_title(path), path=path)
        for path in sorted(reports_dir.glob("*.py"))
        if not path.name.startswith("_")
    ]


def print_menu(reports: list[Report], file=None) -> None:
    print(f"Доступные отчёты ({REPORTS_DIR}):", file=file)
    for number, report in enumerate(reports, start=1):
        print(f"  {number}) {report.id} — {report.title}", file=file)


def choose(reports: list[Report]) -> Report | None:
    """Выбор отчёта из stdin: номер, id или выход (q/0/пустая строка/EOF)."""
    try:
        choice = input("Выберите отчёт (номер или id, q — выход): ").strip()
    except EOFError:
        print()
        return None

    if not choice or choice.lower() in ("q", "quit", "0"):
        return None

    if choice.isdigit():
        number = int(choice)
        if 1 <= number <= len(reports):
            return reports[number - 1]
    else:
        for report in reports:
            if report.id == choice:
                return report

    print(f"Нет такого отчёта: {choice}", file=sys.stderr)
    return None


def run_report(report: Report, args: list[str]) -> int:
    return subprocess.call([sys.executable, str(report.path), *args])


def main() -> int:
    argv = sys.argv[1:]

    if "-h" in argv or "--help" in argv:
        print(__doc__)
        return 0

    reports = discover_reports()
    if not reports:
        print(f"Отчёты не найдены: {REPORTS_DIR}", file=sys.stderr)
        return 1

    if "--list" in argv:
        for report in reports:
            print(f"{report.id}\t{report.title}")
        return 0

    # Прямой выбор без меню: первый аргумент — id отчёта, остальное пробрасывается.
    if argv and not argv[0].startswith("-"):
        by_id = {report.id: report for report in reports}
        if argv[0] not in by_id:
            print(f"Отчёт не найден: {argv[0]}", file=sys.stderr)
            print_menu(reports, file=sys.stderr)
            return 1

        return run_report(by_id[argv[0]], argv[1:])

    print_menu(reports)
    report = choose(reports)
    if report is None:
        print("Выход.")
        return 0

    # Аргументы лаунчера (без id) пробрасываются выбранному отчёту.
    launcher_args = [arg for arg in argv if arg != "--"]
    return run_report(report, launcher_args)


if __name__ == "__main__":
    sys.exit(main())
