#!/usr/bin/env python3
"""Автотесты инфраструктуры Python-отчётов: меню, дискавери, шаблон, пример.

Запуск: make test (python3 -m unittest discover -s scripts/tests).
Новые отчёты сопровождайте тестами здесь — см. docs/development.md.
"""

from __future__ import annotations

import ast
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

SCRIPTS_DIR = Path(__file__).resolve().parent.parent
REPORTS_DIR = SCRIPTS_DIR / "reports"
LAUNCHER = SCRIPTS_DIR / "report.py"

sys.path.insert(0, str(SCRIPTS_DIR))
import report  # noqa: E402 — scripts/report.py


def make_test_db(path: Path) -> None:
    """Синтетическая база с минимальной схемой для отчётов."""
    connection = sqlite3.connect(path)
    connection.executescript(
        """
        create table receipts (
            key text primary key,
            user_phone text,
            kkt_owner text,
            receive_date text,
            total_sum text
        );
        create table fiscal_data (
            receipt_key text primary key,
            date_time text,
            total_sum real
        );
        insert into receipts values
            ('r1', '79000000001', 'Магазин 1', '2026-09-01 10:00:00', '100.50'),
            ('r2', '79000000001', 'Магазин 2', '2026-09-05 11:00:00', '200.00');
        """
    )
    connection.commit()
    connection.close()


def run_python(script: Path, *args: str, stdin: str | None = None) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(script), *args],
        input=stdin,
        capture_output=True,
        text=True,
    )


class DiscoveryTests(unittest.TestCase):
    def test_reports_discovered_and_private_skipped(self):
        ids = [entry.id for entry in report.discover_reports(REPORTS_DIR)]
        self.assertIn("lkdr_report", ids)
        self.assertIn("example", ids)
        self.assertNotIn("_template", ids)

    def test_sorted_by_id(self):
        ids = [entry.id for entry in report.discover_reports(REPORTS_DIR)]
        self.assertEqual(ids, sorted(ids))

    def test_title_from_docstring_first_line(self):
        by_id = {entry.id: entry for entry in report.discover_reports(REPORTS_DIR)}
        self.assertTrue(by_id["example"].title.startswith("Пример отчёта"))
        self.assertTrue(by_id["lkdr_report"].title)

    def test_template_is_valid_python(self):
        ast.parse((REPORTS_DIR / "_template.py").read_text(encoding="utf-8"))


class LauncherTests(unittest.TestCase):
    def test_list(self):
        proc = run_python(LAUNCHER, "--list")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("example", proc.stdout)
        self.assertIn("lkdr_report", proc.stdout)
        self.assertNotIn("_template", proc.stdout)

    def test_direct_run_by_id(self):
        with tempfile.TemporaryDirectory() as tmp:
            db = Path(tmp) / "test.db"
            make_test_db(db)
            proc = run_python(LAUNCHER, "example", "--db", str(db))
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertIn("Чеков: 2", proc.stdout)

    def test_menu_choice_by_id(self):
        with tempfile.TemporaryDirectory() as tmp:
            db = Path(tmp) / "test.db"
            make_test_db(db)
            proc = run_python(LAUNCHER, "--db", str(db), stdin="example\n")
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertIn("Чеков: 2", proc.stdout)

    def test_menu_quit(self):
        proc = run_python(LAUNCHER, stdin="q\n")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("Выход", proc.stdout)

    def test_unknown_report_fails(self):
        proc = run_python(LAUNCHER, "nosuchreport")
        self.assertEqual(proc.returncode, 1)
        self.assertIn("Отчёт не найден", proc.stderr)


class ExampleReportTests(unittest.TestCase):
    def run_example(self, *args: str) -> subprocess.CompletedProcess[str]:
        return run_python(REPORTS_DIR / "example.py", *args)

    def test_summary_output(self):
        with tempfile.TemporaryDirectory() as tmp:
            db = Path(tmp) / "test.db"
            make_test_db(db)
            proc = self.run_example("--db", str(db))
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertIn("Чеков: 2", proc.stdout)
            self.assertIn("Магазин 1", proc.stdout)
            self.assertIn("Магазин 2", proc.stdout)
            self.assertIn("300.50", proc.stdout)

    def test_top_limit(self):
        with tempfile.TemporaryDirectory() as tmp:
            db = Path(tmp) / "test.db"
            make_test_db(db)
            proc = self.run_example("--db", str(db), "--top", "1")
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertIn("Магазин 2", proc.stdout)
            self.assertNotIn("Магазин 1:", proc.stdout)

    def test_missing_db_fails_cleanly(self):
        proc = self.run_example("--db", "/nonexistent/lkdr.db")
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("не найдена", proc.stderr)


if __name__ == "__main__":
    unittest.main()
