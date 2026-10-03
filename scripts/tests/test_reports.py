#!/usr/bin/env python3
"""Автотесты инфраструктуры Python-отчётов: меню, дискавери, шаблон, пример.

Запуск: make test (python3 -m unittest discover -s scripts/tests).
Новые отчёты сопровождайте тестами здесь — см. docs/development.md.
"""

from __future__ import annotations

import ast
import json
import os
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
sys.path.insert(0, str(REPORTS_DIR))
import _config  # noqa: E402 — scripts/reports/_config.py
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


def run_python(
    script: Path,
    *args: str,
    stdin: str | None = None,
    env: dict[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, str(script), *args],
        input=stdin,
        capture_output=True,
        text=True,
        env=env,
    )


def write_fake_agent(directory: Path, name: str, output: str) -> Path:
    """Исполняемый скрипт-агент: съедает stdin, печатает заданный текст."""
    script = directory / name
    script.write_text(
        "#!/bin/sh\ncat >/dev/null\ncat <<'AGENT_EOF'\n" + output + "\nAGENT_EOF\n",
        encoding="utf-8",
    )
    script.chmod(0o755)
    return script


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


def make_ai_test_db(path: Path) -> None:
    """Синтетическая база для HTML-отчёта: два периода, возврат, предоплата."""
    connection = sqlite3.connect(path)
    connection.executescript(
        """
        create table receipts (
            key text primary key, user_phone text, buyer text, buyer_type text,
            created_date text, fiscal_document_number text, fiscal_drive_number text,
            kkt_owner text, kkt_owner_inn text, receive_date text, total_sum text,
            brand_id integer
        );
        create table brands (id integer primary key, name text, description text, image text);
        create table fiscal_data (
            receipt_key text primary key, date_time text, total_sum real,
            operation_type integer, prepaid_sum real, retail_place text,
            retail_place_address text, user text, user_inn text
        );
        create table fiscal_data_items (
            receipt_key text, db_idx integer, name text, nds integer,
            payment_type integer, price real, product_type integer,
            provider_inn text, quantity real, sum real,
            primary key (receipt_key, db_idx)
        );
        """
    )
    receipts = [
        ("p1", "2026-08-01 12:00:00", 800.0, 1, 0.0, "Магазин А"),
        ("c1", "2026-09-01 12:00:00", 1000.0, 1, 0.0, "Магазин А"),
        ("c2", "2026-09-05 15:00:00", 600.0, 1, 0.0, "Магазин Б"),
        ("c3", "2026-09-08 18:00:00", 300.0, 2, 0.0, "Магазин Б"),
        ("c4", "2026-09-09 10:00:00", 500.0, 1, 500.0, "Магазин А"),
        ("c5", "2026-09-10 20:00:00", 400.0, 1, 0.0, "Магазин В"),
    ]
    for key, when, total, operation, prepaid, store in receipts:
        connection.execute(
            "insert into receipts values (?,?,?,?,?,?,?,?,?,?,?,NULL)",
            (key, "79000000001", None, "INDIVIDUAL", when, "1", "d" + key, store, "7700000001", when, str(total)),
        )
        connection.execute(
            "insert into fiscal_data values (?,?,?,?,?,?,?,?,?)",
            (key, when, total, operation, prepaid, store, "г. Москва", store, "7700000001"),
        )

    items = [
        ("p1", "Молоко 3.2%", 2, 100.0), ("p1", "Сыр российский", 1, 700.0),
        ("c1", "Молоко 3.2%", 4, 200.0), ("c1", "Сыр российский", 1, 300.0), ("c1", "Кофе в зернах", 1, 500.0),
        ("c2", "Молоко 3.2%", 2, 100.0), ("c2", "Хлеб бородинский", 3, 120.0),
        ("c3", "Хлеб бородинский", -1, -40.0),
        ("c5", "Кофе в зернах", 1, 400.0),
    ]
    counters: dict[str, int] = {}
    for key, name, quantity, total in items:
        counters[key] = counters.get(key, 0) + 1
        connection.execute(
            "insert into fiscal_data_items values (?,?,?,?,?,?,?,?,?,?)",
            (key, counters[key], name, 10, 4, abs(total / quantity) if quantity else 0, 1, None, quantity, total),
        )
    connection.commit()
    connection.close()


class AiReportTests(unittest.TestCase):
    def run_ai_report(self, *args: str) -> subprocess.CompletedProcess[str]:
        return run_python(REPORTS_DIR / "ai_report.py", *args)

    def test_creates_month_file_without_placeholders(self):
        with tempfile.TemporaryDirectory() as tmp:
            db = Path(tmp) / "lkdr.db"
            out = Path(tmp) / "reports"
            make_ai_test_db(db)
            proc = self.run_ai_report("--db", str(db), "--out-dir", str(out), "--no-ai")
            self.assertEqual(proc.returncode, 0, proc.stderr)

            report = out / "lkdr-2026-09.html"
            self.assertTrue(report.exists(), proc.stdout)
            content = report.read_text(encoding="utf-8")
            self.assertIn("Магазин А", content)
            self.assertIn("1 700.00 ₽", content)
            self.assertIn("Месяц в чеках", content)
            self.assertNotIn("{{", content)
            self.assertNotIn("<!--", content)
            # Текст комментариев шаблона не должен утекать в отчёт.
            self.assertNotIn("-->", content)
            self.assertNotIn("ШАБЛОН-ПРОТОТИП", content)
            self.assertNotIn("генератор (scripts/reports/ai_report.py)", content)

    def test_rerun_updates_same_month_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            db = Path(tmp) / "lkdr.db"
            out = Path(tmp) / "reports"
            make_ai_test_db(db)
            self.run_ai_report("--db", str(db), "--out-dir", str(out), "--no-ai")

            connection = sqlite3.connect(db)
            connection.execute(
                "insert into receipts values ('c6','79000000001',NULL,'INDIVIDUAL','2026-09-12 10:00:00','1','dc6','Новый Магазин','7700000001','2026-09-12 10:00:00','250.0',NULL)"
            )
            connection.execute(
                "insert into fiscal_data values ('c6','2026-09-12 10:00:00',250.0,1,0.0,'Новый Магазин','г. Москва','Новый Магазин','7700000001')"
            )
            connection.commit()
            connection.close()

            proc = self.run_ai_report("--db", str(db), "--out-dir", str(out), "--no-ai")
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertEqual(list(out.iterdir()), [out / "lkdr-2026-09.html"])
            self.assertIn("Новый Магазин", (out / "lkdr-2026-09.html").read_text(encoding="utf-8"))

    def test_as_of_selects_other_month(self):
        with tempfile.TemporaryDirectory() as tmp:
            db = Path(tmp) / "lkdr.db"
            out = Path(tmp) / "reports"
            make_ai_test_db(db)
            proc = self.run_ai_report(
                "--db", str(db), "--out-dir", str(out), "--no-ai", "--as-of", "2026-08-15 12:00"
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            self.assertTrue((out / "lkdr-2026-08.html").exists(), proc.stdout)

    def test_missing_db_fails_cleanly(self):
        proc = self.run_ai_report("--db", "/nonexistent/lkdr.db", "--out-dir", "/tmp/lf-ai-missing")
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("не найдена", proc.stderr)


class AiCommandConfigTests(unittest.TestCase):
    def test_load_ai_command_from_config(self):
        with tempfile.TemporaryDirectory() as tmp:
            config = Path(tmp) / "config.json"
            config.write_text('{"ai": {"command": "claude -p"}}', encoding="utf-8")
            self.assertEqual(_config.load_ai_command(config), "claude -p")

    def test_missing_file_returns_default(self):
        self.assertEqual(
            _config.load_ai_command(Path("/nonexistent/config.json")),
            _config.DEFAULT_AI_COMMAND,
        )

    def test_missing_key_returns_default(self):
        with tempfile.TemporaryDirectory() as tmp:
            config = Path(tmp) / "config.json"
            config.write_text("{}", encoding="utf-8")
            self.assertEqual(_config.load_ai_command(config), _config.DEFAULT_AI_COMMAND)

    def test_custom_default(self):
        self.assertEqual(
            _config.load_ai_command(Path("/nonexistent/config.json"), default="myagent"),
            "myagent",
        )


class AiAgentTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.env = {**os.environ, "PATH": f"{self.bin}{os.pathsep}{os.environ['PATH']}"}

    def test_ai_report_uses_configured_agent(self):
        payload = json.dumps(
            {
                "lead": "Вывод фейк-агента",
                "cards": [{"theme": "Тема", "title": "Карточка из фейк-агента", "body": "Тело карточки."}],
                "actions": [{"head": "Действие", "body": "Описание действия."}],
            },
            ensure_ascii=False,
        )
        write_fake_agent(self.bin, "fakeai", payload)
        (self.root / "config.json").write_text('{"ai": {"command": "fakeai"}}', encoding="utf-8")
        db = self.root / "lkdr.db"
        make_ai_test_db(db)

        proc = run_python(
            REPORTS_DIR / "ai_report.py",
            "--db", str(db),
            "--out-dir", str(self.root / "reports"),
            "--config", str(self.root / "config.json"),
            env=self.env,
        )

        self.assertEqual(proc.returncode, 0, proc.stderr)
        content = (self.root / "reports" / "lkdr-2026-09.html").read_text(encoding="utf-8")
        self.assertIn("Карточка из фейк-агента", content)
        self.assertIn("Вывод фейк-агента", content)
        self.assertIn("AI CLI (fakeai)", content)

    def test_ai_report_flag_overrides_config(self):
        payload = json.dumps(
            {
                "lead": "Флаг важнее конфига",
                "cards": [{"theme": "Т", "title": "Карточка по флагу", "body": "Тело."}],
                "actions": [],
            },
            ensure_ascii=False,
        )
        write_fake_agent(self.bin, "fakeflag", payload)
        (self.root / "config.json").write_text('{"ai": {"command": "no-such-agent"}}', encoding="utf-8")
        db = self.root / "lkdr.db"
        make_ai_test_db(db)

        proc = run_python(
            REPORTS_DIR / "ai_report.py",
            "--db", str(db),
            "--out-dir", str(self.root / "reports"),
            "--config", str(self.root / "config.json"),
            "--ai-command", "fakeflag",
            env=self.env,
        )

        self.assertEqual(proc.returncode, 0, proc.stderr)
        content = (self.root / "reports" / "lkdr-2026-09.html").read_text(encoding="utf-8")
        self.assertIn("Карточка по флагу", content)
        self.assertIn("AI CLI (fakeflag)", content)

    def test_lkdr_report_uses_configured_agent(self):
        write_fake_agent(self.bin, "fakeai", "ТЕСТ_AI_ВЫВОД_12345")
        (self.root / "config.json").write_text('{"ai": {"command": "fakeai"}}', encoding="utf-8")
        db = self.root / "lkdr.db"
        make_ai_test_db(db)

        proc = run_python(
            REPORTS_DIR / "lkdr_report.py",
            "--db", str(db),
            "--config", str(self.root / "config.json"),
            "--ai-summary",
            "--color", "never",
            env=self.env,
        )

        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("ТЕСТ_AI_ВЫВОД_12345", proc.stdout)


LONG_ITEM_NAME = "Очень длинное название товара для проверки обрезки в отчётах"


def truncated_name(limit: int) -> str:
    return LONG_ITEM_NAME[: limit - 3].rstrip() + "..."


def add_long_item(db: Path) -> None:
    connection = sqlite3.connect(db)
    connection.execute(
        "insert into receipts values ('c8','79000000001',NULL,'INDIVIDUAL','2026-09-11 10:00:00','1','dc8','Магазин Г','7700000001','2026-09-11 10:00:00','150.0',NULL)"
    )
    connection.execute(
        "insert into fiscal_data values ('c8','2026-09-11 10:00:00',150.0,1,0.0,'Магазин Г','г. Москва','Магазин Г','7700000001')"
    )
    connection.execute(
        "insert into fiscal_data_items values ('c8',1,?,10,4,75.0,1,NULL,2,150.0)",
        (LONG_ITEM_NAME,),
    )
    connection.commit()
    connection.close()


class ItemNameCharsConfigTests(unittest.TestCase):
    def test_missing_file_returns_default(self):
        self.assertEqual(
            _config.load_max_item_name_chars(Path("/nonexistent/config.json")),
            _config.DEFAULT_MAX_ITEM_NAME_CHARS,
        )

    def test_missing_key_returns_default(self):
        with tempfile.TemporaryDirectory() as tmp:
            config = Path(tmp) / "config.json"
            config.write_text("{}", encoding="utf-8")
            self.assertEqual(_config.load_max_item_name_chars(config), 40)

    def test_custom_value(self):
        with tempfile.TemporaryDirectory() as tmp:
            config = Path(tmp) / "config.json"
            config.write_text('{"reports": {"maxItemNameChars": 12}}', encoding="utf-8")
            self.assertEqual(_config.load_max_item_name_chars(config), 12)

    def test_invalid_value_raises(self):
        with tempfile.TemporaryDirectory() as tmp:
            config = Path(tmp) / "config.json"
            for value in ("-5", '"40"', "true"):
                config.write_text(
                    json.dumps({"reports": {"maxItemNameChars": json.loads(value)}}),
                    encoding="utf-8",
                )
                with self.assertRaises(ValueError):
                    _config.load_max_item_name_chars(config)


class ItemNameTruncationTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self.db = self.root / "lkdr.db"
        make_ai_test_db(self.db)
        add_long_item(self.db)

    def test_ai_report_truncates_to_default_40(self):
        proc = run_python(
            REPORTS_DIR / "ai_report.py",
            "--db", str(self.db),
            "--out-dir", str(self.root / "reports"),
            "--no-ai",
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        content = (self.root / "reports" / "lkdr-2026-09.html").read_text(encoding="utf-8")
        self.assertIn(truncated_name(40), content)
        self.assertNotIn(LONG_ITEM_NAME, content)

    def test_ai_report_respects_config_limit(self):
        config = self.root / "config.json"
        config.write_text('{"reports": {"maxItemNameChars": 10}}', encoding="utf-8")
        proc = run_python(
            REPORTS_DIR / "ai_report.py",
            "--db", str(self.db),
            "--out-dir", str(self.root / "reports"),
            "--config", str(config),
            "--no-ai",
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        content = (self.root / "reports" / "lkdr-2026-09.html").read_text(encoding="utf-8")
        self.assertIn(truncated_name(10), content)
        self.assertNotIn(LONG_ITEM_NAME, content)

    def test_lkdr_report_truncates_to_default_40(self):
        proc = run_python(
            REPORTS_DIR / "lkdr_report.py",
            "--db", str(self.db),
            "--color", "never",
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn(truncated_name(40), proc.stdout)
        self.assertNotIn(LONG_ITEM_NAME, proc.stdout)

    def test_lkdr_report_respects_config_limit(self):
        config = self.root / "config.json"
        config.write_text('{"reports": {"maxItemNameChars": 10}}', encoding="utf-8")
        proc = run_python(
            REPORTS_DIR / "lkdr_report.py",
            "--db", str(self.db),
            "--config", str(config),
            "--color", "never",
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn(truncated_name(10), proc.stdout)
        self.assertNotIn(LONG_ITEM_NAME, proc.stdout)

    def test_invalid_config_fails_loudly(self):
        config = self.root / "config.json"
        config.write_text('{"reports": {"maxItemNameChars": 0}}', encoding="utf-8")
        proc = run_python(
            REPORTS_DIR / "ai_report.py",
            "--db", str(self.db),
            "--out-dir", str(self.root / "reports"),
            "--config", str(config),
            "--no-ai",
        )
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("maxItemNameChars", proc.stderr)


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
