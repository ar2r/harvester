#!/usr/bin/env python3
"""Отчёт по покупкам из SQLite-базы LKDR."""

from __future__ import annotations

import argparse
import re
import shutil
import sqlite3
import subprocess
import sys
from collections import defaultdict
from collections.abc import Iterable, Mapping
from dataclasses import dataclass, field
from datetime import datetime, timedelta
from pathlib import Path


DEFAULT_DB = "lkdr.db"
DEFAULT_DAYS = 30
MAX_TABLE_CELL_WIDTH = 75
ANSI_RE = re.compile(r"\x1b\[[0-9;]*m")
MARKDOWN_HEADING_RE = re.compile(r"^\s*\*\*(\d+\.\s+[^*]+)\*\*\s*$")
MARKDOWN_BOLD_RE = re.compile(r"\*\*([^*]+)\*\*")


class Color:
    def __init__(self, enabled: bool) -> None:
        self.enabled = enabled

    def apply(self, value: object, code: str) -> str:
        text = str(value)
        if not self.enabled:
            return text
        return f"\033[{code}m{text}\033[0m"

    def header(self, value: object) -> str:
        return self.apply(value, "1;34")

    def positive(self, value: object) -> str:
        return self.apply(value, "32")

    def negative(self, value: object) -> str:
        return self.apply(value, "31")

    def warning(self, value: object) -> str:
        return self.apply(value, "33")

    def muted(self, value: object) -> str:
        return self.apply(value, "90")


COLOR = Color(False)


@dataclass
class MutableStats:
    count: int = 0
    total: float = 0
    gross_total: float = 0
    ignored_count: int = 0
    ignored_total: float = 0
    refund_count: int = 0
    refund_total: float = 0


@dataclass
class ItemStats:
    quantity: float = 0
    total: float = 0
    purchase_receipts: set[str] = field(default_factory=set)
    refund_receipts: set[str] = field(default_factory=set)


@dataclass
class PeriodReport:
    start: datetime
    end: datetime
    stats_by_currency: defaultdict[str, MutableStats]
    stores: defaultdict[tuple[str, str], MutableStats]
    days_total: defaultdict[tuple[str, str], MutableStats]
    refund_stores: defaultdict[tuple[str, str], MutableStats]
    items: defaultdict[tuple[str, str], ItemStats]


CURRENCY_SYMBOLS = {
    "RUB": "₽",
    "KZT": "₸",
}

CURRENCY_NAMES = {
    "RUB": "рубли",
    "KZT": "тенге",
}

KAZAKHSTAN_MARKERS = (
    "казахстан",
    "kazakhstan",
    "алматы",
    "астана",
    "almaty",
    "astana",
    ".kz",
)

SERVICE_ITEM_MARKERS = (
    "аванс",
    "доставка",
    "доставк",
    "курьер",
    "упаковка заказа",
    "компенсация",
    "возврат",
    "агентское вознаграждение",
    "перевозка",
    "расходы по поручению",
    "услуги связи",
)

CATEGORY_RULES = (
    (
        "Молочные продукты",
        (
            "молоко",
            "кефир",
            "ряженка",
            "сметан",
            "йогурт",
            "творог",
            "сырок",
            "сыр",
            "сливк",
            "масло",
        ),
    ),
    (
        "Мясо и птица",
        (
            "мясо",
            "курица",
            "цыплен",
            "индейк",
            "говядина",
            "свинина",
            "фарш",
            "котлет",
            "колбас",
            "сосиск",
            "ветчин",
        ),
    ),
    (
        "Рыба и морепродукты",
        (
            "рыба",
            "лосос",
            "форель",
            "треск",
            "тунец",
            "кревет",
            "морепродукт",
            "икра",
        ),
    ),
    (
        "Овощи и фрукты",
        (
            "овощ",
            "фрукт",
            "картоф",
            "томат",
            "помидор",
            "огур",
            "морков",
            "лук",
            "капуст",
            "салат",
            "зелень",
            "яблок",
            "банан",
            "груш",
            "апельсин",
            "мандарин",
            "ягод",
        ),
    ),
    (
        "Хлеб и выпечка",
        (
            "хлеб",
            "батон",
            "булочка",
            "лаваш",
            "пирог",
            "круассан",
            "выпеч",
        ),
    ),
    (
        "Бакалея",
        (
            "яйцо",
            "крупа",
            "греч",
            "рис",
            "макарон",
            "мука",
            "масло раст",
            "сахар",
            "соль",
            "соус",
            "спец",
            "консерв",
            "хлоп",
            "мюсли",
        ),
    ),
    (
        "Напитки",
        (
            "чай",
            "кофе",
            "сок",
            "вода",
            "морс",
            "лимонад",
            "напит",
        ),
    ),
    (
        "Сладости и снеки",
        (
            "шоколад",
            "печень",
            "конфет",
            "вафл",
            "морожен",
            "чипс",
            "снэк",
            "снек",
            "пирожн",
        ),
    ),
    (
        "Готовая еда",
        (
            "пицца",
            "ролл",
            "суши",
            "бургер",
            "шаурм",
            "кофейня",
            "кафе",
            "ресторан",
            "яндекс еда",
            "delivery",
            "додо",
        ),
    ),
    (
        "Дом и ремонт",
        (
            "смеситель",
            "термостат",
            "лампа",
            "светильник",
            "розетка",
            "кабель",
            "краска",
            "инструмент",
            "шуруп",
            "сантех",
            "ванн",
            "душ",
            "кухн",
            "мебель",
            "икеа",
            "леруа",
        ),
    ),
    (
        "Одежда и обувь",
        (
            "трусы",
            "носки",
            "футболка",
            "рубашка",
            "брюки",
            "джинсы",
            "куртка",
            "платье",
            "кроссов",
            "ботин",
            "обув",
            "одежд",
            "omsа",
            "omsa",
        ),
    ),
    (
        "Бытовая химия",
        (
            "порошок",
            "гель для стир",
            "кондиционер для белья",
            "средство для",
            "чистящ",
            "моющ",
            "мыло",
            "шампун",
            "зубная паста",
            "дезодорант",
            "салфет",
            "бумага туалет",
        ),
    ),
    (
        "Аптека и здоровье",
        (
            "аптека",
            "лекар",
            "таблет",
            "витамин",
            "спрей",
            "сироп",
            "бинт",
            "пластыр",
            "линзы",
        ),
    ),
    (
        "Электроника",
        (
            "телефон",
            "смартфон",
            "ноутбук",
            "планшет",
            "заряд",
            "наушник",
            "кабель usb",
            "аккумулятор",
            "батарей",
        ),
    ),
    (
        "Транспорт",
        (
            "такси",
            "метро",
            "автобус",
            "бензин",
            "топливо",
            "парков",
            "проезд",
        ),
    ),
    (
        "Сервисы и комиссии",
        SERVICE_ITEM_MARKERS,
    ),
)


def parse_datetime(value: str) -> datetime:
    return datetime.fromisoformat(value)


def money(value: float | int | None, currency: str) -> str:
    symbol = CURRENCY_SYMBOLS.get(currency, currency)
    return f"{float(value or 0):,.2f}".replace(",", " ") + f" {symbol}"


def percent(value: float, total: float) -> str:
    if total == 0:
        return "0.0%"
    return f"{value / total * 100:.1f}%"


def percent_delta(value: float, previous: float) -> str:
    if previous == 0:
        return "н/д"
    delta = (value - previous) / previous * 100
    sign = "+" if delta > 0 else ""
    return f"{sign}{delta:.1f}%"


def share_bar(value: float, total: float, width: int = 10) -> str:
    if total <= 0:
        return f"{'░' * width} 0.0%"

    ratio = max(0.0, min(value / total, 1.0))
    filled = min(width, max(1, int(ratio * width + 0.999))) if value > 0 else 0
    bar = "█" * filled + "░" * (width - filled)
    if ratio >= 0.25:
        bar = COLOR.warning(bar)
    else:
        bar = COLOR.muted(bar)
    return f"{bar} {percent(value, total)}"


def print_table(headers: tuple[str, ...], rows: Iterable[tuple[object, ...]]) -> None:
    rows = [tuple(truncate_cell(cell) for cell in row) for row in rows]
    if not rows:
        print(COLOR.muted("(нет данных)"))
        return

    widths = [visible_len(header) for header in headers]
    for row in rows:
        widths = [max(width, visible_len(cell)) for width, cell in zip(widths, row)]

    print(format_row(headers, widths))
    print(COLOR.muted(format_row(tuple("-" * width for width in widths), widths)))
    for row in rows:
        print(format_row(row, widths))


def visible_len(value: object) -> int:
    return len(ANSI_RE.sub("", str(value)))


def truncate_cell(value: object, limit: int = MAX_TABLE_CELL_WIDTH) -> str:
    text = str(value)
    if visible_len(text) <= limit:
        return text
    if ANSI_RE.search(text):
        return text
    return text[: limit - 3].rstrip() + "..."


def pad_cell(value: object, width: int) -> str:
    text = str(value)
    return text + " " * (width - visible_len(text))


def format_row(row: tuple[object, ...], widths: list[int]) -> str:
    return "  ".join(pad_cell(cell, width) for cell, width in zip(row, widths))


def normalize_currency(value: str) -> str:
    currency = value.strip().upper()
    aliases = {
        "RUR": "RUB",
        "₽": "RUB",
        "РУБ": "RUB",
        "РУБЛЬ": "RUB",
        "РУБЛИ": "RUB",
        "ТГ": "KZT",
        "₸": "KZT",
        "ТЕНГЕ": "KZT",
    }
    return aliases.get(currency, currency)


def parse_currency_rule(value: str) -> tuple[str, str]:
    if "=" not in value:
        raise argparse.ArgumentTypeError("Expected NAME=CURRENCY")

    name, currency = value.split("=", 1)
    name = name.strip()
    currency = normalize_currency(currency)
    if not name or not currency:
        raise argparse.ArgumentTypeError("Expected NAME=CURRENCY")
    return name, currency


def build_rule_map(rules: Iterable[tuple[str, str]]) -> dict[str, str]:
    return {name.casefold(): currency for name, currency in rules}


def require_tables(conn: sqlite3.Connection) -> None:
    required = {"receipts", "brands", "fiscal_data", "fiscal_data_items"}
    existing = {
        row[0]
        for row in conn.execute(
            "select name from sqlite_master where type = 'table' and name in ({})".format(
                ",".join("?" for _ in required)
            ),
            tuple(required),
        )
    }
    missing = sorted(required - existing)
    if missing:
        raise SystemExit(f"Database schema is missing required tables: {', '.join(missing)}")


def latest_receipt_datetime(conn: sqlite3.Connection) -> datetime:
    row = conn.execute("select max(date_time) from fiscal_data").fetchone()
    if not row or row[0] is None:
        raise SystemExit("No fiscal data found in database")
    return parse_datetime(row[0])


def store_name(row: sqlite3.Row) -> str:
    return row["store"] or row["kkt_owner"] or row["fd_user"] or "Unknown"


def detect_currency(
    row: sqlite3.Row,
    store_rules: Mapping[str, str],
    receipt_rules: Mapping[str, str],
) -> str:
    receipt_key = str(row["receipt_key"]).casefold()
    if receipt_key in receipt_rules:
        return receipt_rules[receipt_key]

    store = store_name(row).casefold()
    if store in store_rules:
        return store_rules[store]

    text = " ".join(
        str(row[key] or "")
        for key in (
            "store",
            "kkt_owner",
            "fd_user",
            "user_inn",
            "retail_place",
            "retail_place_address",
        )
    ).casefold()
    if any(marker in text for marker in KAZAKHSTAN_MARKERS):
        return "KZT"

    return "RUB"


def effective_spend(row: sqlite3.Row) -> float:
    if operation_sign(row) < 0:
        return float(row["total_sum"] or 0)
    return max(float(row["total_sum"] or 0) - float(row["prepaid_sum"] or 0), 0)


def operation_sign(row: sqlite3.Row) -> int:
    operation_type = int(row["operation_type"] or 1)
    if operation_type in (2, 3):
        return -1
    return 1


def signed_effective_spend(row: sqlite3.Row) -> float:
    return operation_sign(row) * effective_spend(row)


def is_service_item(name: str) -> bool:
    normalized = name.casefold()
    return any(marker in normalized for marker in SERVICE_ITEM_MARKERS)


def categorize_item(name: str) -> str:
    normalized = name.casefold()
    for category, markers in CATEGORY_RULES:
        if any(marker in normalized for marker in markers):
            return category
    return "Прочее"


def category_totals(item_totals: Mapping[str, float]) -> defaultdict[str, float]:
    totals: defaultdict[str, float] = defaultdict(float)
    for name, total in item_totals.items():
        if total > 0:
            totals[categorize_item(name)] += total
    return totals


def print_header(title: str) -> None:
    print(COLOR.header(title))
    print(COLOR.muted("-" * visible_len(title)))


def render_ai_output(text: str) -> str:
    rendered: list[str] = []
    for line in text.splitlines():
        heading = MARKDOWN_HEADING_RE.match(line)
        if heading:
            title = heading.group(1).strip()
            rendered.append(COLOR.header(title))
            rendered.append(COLOR.muted("-" * visible_len(title)))
            continue

        rendered.append(MARKDOWN_BOLD_RE.sub(r"\1", line))
    return "\n".join(rendered).strip()


def money_delta(current: float, previous: float, currency: str) -> str:
    delta = current - previous
    sign = "+" if delta > 0 else ""
    return f"{sign}{money(delta, currency)}"


def colored_expense_delta(current: float, previous: float, currency: str) -> str:
    delta = current - previous
    value = money_delta(current, previous, currency)
    if delta < 0:
        return COLOR.positive(value)
    if delta > 0:
        return COLOR.negative(value)
    return value


def colored_neutral_money_delta(current: float, previous: float, currency: str) -> str:
    delta = current - previous
    value = money_delta(current, previous, currency)
    if delta > 0:
        return COLOR.positive(value)
    if delta < 0:
        return COLOR.negative(value)
    return value


def count_delta(current: int, previous: int) -> str:
    delta = current - previous
    sign = "+" if delta > 0 else ""
    return f"{sign}{delta}"


def insight_line(label: str, current: float, previous: float, currency: str) -> str:
    delta = current - previous
    delta_text = f"{money_delta(current, previous, currency)} ({percent_delta(current, previous)})"
    if delta < 0:
        delta_text = COLOR.positive(delta_text)
    elif delta > 0:
        delta_text = COLOR.negative(delta_text)
    return f"{label}: {money(current, currency)} против {money(previous, currency)} ({delta_text})"


def changed_rows(
    current_values: Mapping[str, float],
    previous_values: Mapping[str, float],
    limit: int,
) -> list[tuple[str, float, float]]:
    names = set(current_values) | set(previous_values)
    rows = [
        (name, current_values.get(name, 0.0), previous_values.get(name, 0.0))
        for name in names
    ]
    rows.sort(key=lambda row: abs(row[1] - row[2]), reverse=True)
    return rows[:limit]


def compact_money_delta(current: float, previous: float, currency: str) -> str:
    return f"{money(current, currency)} / было {money(previous, currency)} / изменение {money_delta(current, previous, currency)}"


def build_ai_prompt(
    *,
    currency_label: str,
    currency: str,
    days: int,
    current_start: datetime,
    current_end: datetime,
    previous_start: datetime,
    previous_end: datetime,
    stats: MutableStats,
    previous_stats: MutableStats,
    avg_day: float,
    previous_avg_day: float,
    previous_item_totals: Mapping[str, float],
    category_rows: list[tuple[str, float, float]],
    store_changes: list[tuple[str, float, float]],
    item_changes: list[tuple[str, float, float]],
    store_rows: list[tuple[str, int, float, float]],
    item_rows: list[tuple[str, float, float]],
    service_rows: list[tuple[str, float, float]],
    recurring_rows: list[tuple[str, int, float, float, float]],
) -> str:
    def lines(title: str, rows: Iterable[str]) -> str:
        body = "\n".join(f"- {row}" for row in rows)
        return f"{title}:\n{body if body else '- нет данных'}"

    store_change_lines = (
        f"{name}: {compact_money_delta(current, previous, currency)}"
        for name, current, previous in store_changes
    )
    item_change_lines = (
        f"{name}: {compact_money_delta(current, previous, currency)}"
        for name, current, previous in item_changes
    )
    store_lines = (
        f"{name}: чеков {count}, {compact_money_delta(total, previous_total, currency)}, доля {percent(total, stats.total)}"
        for name, count, total, previous_total in store_rows
    )
    item_lines = (
        f"{name}: количество {quantity:.3g}, {compact_money_delta(total, previous_total, currency)}, доля {percent(total, stats.total)}"
        for name, quantity, total in item_rows
        for previous_total in (previous_item_totals.get(name, 0.0),)
    )
    service_lines = (
        f"{name}: количество {quantity:.3g}, {compact_money_delta(total, previous_total, currency)}"
        for name, quantity, total in service_rows
        for previous_total in (previous_item_totals.get(name, 0.0),)
    )
    recurring_lines = (
        f"{name}: покупок {purchases}, количество {quantity:.3g}, сумма {compact_money_delta(total, previous_total, currency)}, средняя цена {money(avg_unit, currency)}"
        for name, purchases, quantity, total, avg_unit in recurring_rows
        for previous_total in (previous_item_totals.get(name, 0.0),)
    )
    category_lines = (
        f"{name}: {compact_money_delta(total, previous_total, currency)}, доля {percent(total, stats.total)}"
        for name, total, previous_total in category_rows
    )

    return f"""
Ты финансовый помощник. Проанализируй расходы семьи из 2 взрослых и 2 подростков.
Семья обычно питается дома. Отчет построен по чекам ФНС, уже учтены возвраты/отмены,
дубли закрытия предоплаты интернет-магазинов и разделение валют.

Нужно дать практичное заключение на русском языке. Не пересказывай все таблицы.
Используй только данные ниже, не выдумывай доходы, долги, цели и медицинские рекомендации.
Обязательно оцени, какие категории товаров занимают наибольшую долю расходов в процентах.
Для еды используй детальные категории: молочные продукты, мясо и птица, рыба,
овощи и фрукты, хлеб и выпечка, бакалея, напитки, сладости и снеки.
Формат ответа:
Каждая строка ответа должна быть не длиннее 120 символов, включая маркеры и нумерацию.
1. Краткий вывод в 3-5 пунктов.
2. Структура категорий: какие категории занимают самые большие доли и как это изменилось.
3. Что сильнее всего повлияло на расходы.
4. Повторяющиеся покупки и бытовые привычки.
5. Что проверить вручную.
6. Рекомендации на следующий месяц.

Валюта: {currency_label} ({currency})
Текущий период: {current_start:%Y-%m-%d %H:%M} - {current_end:%Y-%m-%d %H:%M} ({days} дней)
Период сравнения: {previous_start:%Y-%m-%d %H:%M} - {previous_end:%Y-%m-%d %H:%M}

Итоги:
- чистые расходы: {compact_money_delta(stats.total, previous_stats.total, currency)}
- расходы до возвратов: {compact_money_delta(stats.gross_total, previous_stats.gross_total, currency)}
- среднее в день: {compact_money_delta(avg_day, previous_avg_day, currency)}
- покупочные чеки: {stats.count} / было {previous_stats.count} / изменение {count_delta(stats.count, previous_stats.count)}
- возвраты и отмены: {money(stats.refund_total, currency)} / было {money(previous_stats.refund_total, currency)}
- закрытие уже учтенной предоплаты: {stats.ignored_count} чеков на {money(stats.ignored_total, currency)}

{lines("Категории расходов", category_lines)}

{lines("Главные изменения по магазинам", store_change_lines)}

{lines("Главные изменения по товарам", item_change_lines)}

{lines("Топ магазинов текущего периода", store_lines)}

{lines("Топ товаров текущего периода", item_lines)}

{lines("Сервисы и комиссии", service_lines)}

{lines("Повторяющиеся покупки", recurring_lines)}
""".strip()


def print_ai_summary(prompt: str, command: str, timeout: int) -> None:
    executable = shutil.which(command)
    if executable is None:
        print(COLOR.warning(f"Codex CLI не найден: {command}"))
        return

    print_header("AI-выводы и рекомендации")
    try:
        result = subprocess.run(
            [executable, "exec", "--color", "never", "--sandbox", "read-only", "-"],
            input=prompt,
            text=True,
            capture_output=True,
            timeout=timeout,
            check=False,
        )
    except subprocess.TimeoutExpired:
        print(COLOR.warning(f"Codex не ответил за {timeout} секунд"))
        return

    if result.returncode != 0:
        message = result.stderr.strip() or result.stdout.strip() or "неизвестная ошибка"
        print(COLOR.warning(f"Codex завершился с ошибкой: {message}"))
        return

    print(render_ai_output(result.stdout))
    print()


def build_period_report(
    conn: sqlite3.Connection,
    start: datetime,
    end: datetime,
    store_rules: Mapping[str, str],
    receipt_rules: Mapping[str, str],
) -> PeriodReport:
    receipts = conn.execute(
        """
        select
            fd.receipt_key,
            fd.date_time,
            fd.operation_type,
            fd.total_sum,
            fd.prepaid_sum,
            fd.retail_place,
            fd.retail_place_address,
            fd.user as fd_user,
            fd.user_inn,
            r.kkt_owner,
            coalesce(nullif(b.name, ''), nullif(r.kkt_owner, ''), nullif(fd.user, ''), 'Unknown') as store
        from fiscal_data fd
        join receipts r on r.key = fd.receipt_key
        left join brands b on b.id = r.brand_id
        where fd.date_time >= ? and fd.date_time <= ?
        """,
        (start.isoformat(sep=" "), end.isoformat(sep=" ")),
    ).fetchall()

    receipt_currencies: dict[str, str] = {}
    stats_by_currency: defaultdict[str, MutableStats] = defaultdict(MutableStats)
    stores: defaultdict[tuple[str, str], MutableStats] = defaultdict(MutableStats)
    days_total: defaultdict[tuple[str, str], MutableStats] = defaultdict(MutableStats)
    refund_stores: defaultdict[tuple[str, str], MutableStats] = defaultdict(MutableStats)

    for row in receipts:
        currency = detect_currency(row, store_rules, receipt_rules)
        receipt_currencies[row["receipt_key"]] = currency
        stats = stats_by_currency[currency]
        unsigned_spend = effective_spend(row)
        spend = signed_effective_spend(row)
        if unsigned_spend > 0 and operation_sign(row) > 0:
            stats.count += 1
            stats.total += spend
            stats.gross_total += spend
            stores[(currency, store_name(row))].count += 1
            stores[(currency, store_name(row))].total += spend
            day = str(row["date_time"])[:10]
            days_total[(currency, day)].count += 1
            days_total[(currency, day)].total += spend
        elif unsigned_spend > 0 and operation_sign(row) < 0:
            stats.refund_count += 1
            stats.refund_total += unsigned_spend
            stats.total += spend
            stores[(currency, store_name(row))].total += spend
            refund_stores[(currency, store_name(row))].count += 1
            refund_stores[(currency, store_name(row))].total += unsigned_spend
            day = str(row["date_time"])[:10]
            days_total[(currency, day)].total += spend
        elif operation_sign(row) > 0 and float(row["prepaid_sum"] or 0) > 0:
            stats.ignored_count += 1
            stats.ignored_total += float(row["total_sum"] or 0)

    items: defaultdict[tuple[str, str], ItemStats] = defaultdict(ItemStats)
    item_rows = conn.execute(
        """
        select
            fd.receipt_key,
            fd.operation_type,
            fd.total_sum,
            fd.prepaid_sum,
            item.name,
            item.quantity,
            item.sum
        from fiscal_data_items item
        join fiscal_data fd on fd.receipt_key = item.receipt_key
        where fd.date_time >= ? and fd.date_time <= ?
        """,
        (start.isoformat(sep=" "), end.isoformat(sep=" ")),
    ).fetchall()

    for row in item_rows:
        if effective_spend(row) <= 0:
            continue

        currency = receipt_currencies.get(row["receipt_key"], "RUB")
        item = items[(currency, row["name"])]
        sign = operation_sign(row)
        item.quantity += sign * float(row["quantity"] or 0)
        item.total += sign * float(row["sum"] or 0)
        if sign > 0:
            item.purchase_receipts.add(row["receipt_key"])
        else:
            item.refund_receipts.add(row["receipt_key"])

    return PeriodReport(
        start=start,
        end=end,
        stats_by_currency=stats_by_currency,
        stores=stores,
        days_total=days_total,
        refund_stores=refund_stores,
        items=items,
    )


def run_report(
    db_path: Path,
    days: int,
    as_of: datetime | None,
    top: int,
    store_currency_rules: Iterable[tuple[str, str]],
    receipt_currency_rules: Iterable[tuple[str, str]],
    color: str,
    ai_summary: bool,
    ai_command: str,
    ai_timeout: int,
) -> None:
    if days < 1:
        raise SystemExit("--days must be at least 1")
    if top < 1:
        raise SystemExit("--top must be at least 1")
    COLOR.enabled = color == "always" or (color == "auto" and sys.stdout.isatty())

    conn = sqlite3.connect(db_path)
    conn.row_factory = sqlite3.Row
    require_tables(conn)

    end = as_of or latest_receipt_datetime(conn)
    start = end - timedelta(days=days)
    previous_end = start
    previous_start = previous_end - timedelta(days=days)
    store_rules = build_rule_map(store_currency_rules)
    receipt_rules = build_rule_map(receipt_currency_rules)
    current = build_period_report(conn, start, end, store_rules, receipt_rules)
    previous = build_period_report(conn, previous_start, previous_end, store_rules, receipt_rules)

    print_header("Отчет по покупкам")
    print(f"{COLOR.muted('База данных:')} {db_path}")
    print(f"{COLOR.muted('Текущий период:')} {start:%Y-%m-%d %H:%M} - {end:%Y-%m-%d %H:%M}")
    print(f"{COLOR.muted('Период сравнения:')} {previous_start:%Y-%m-%d %H:%M} - {previous_end:%Y-%m-%d %H:%M}")
    print()

    currencies = sorted(set(current.stats_by_currency) | set(previous.stats_by_currency))

    for currency in currencies:
        stats = current.stats_by_currency[currency]
        previous_stats = previous.stats_by_currency[currency]
        currency_label = CURRENCY_NAMES.get(currency, currency)
        avg_receipt = stats.total / stats.count if stats.count else 0
        avg_day = stats.total / days
        previous_avg_receipt = (
            previous_stats.total / previous_stats.count if previous_stats.count else 0
        )
        previous_avg_day = previous_stats.total / days
        current_store_totals = {
            store: value.total
            for (item_currency, store), value in current.stores.items()
            if item_currency == currency and value.total > 0
        }
        previous_store_totals = {
            store: value.total
            for (item_currency, store), value in previous.stores.items()
            if item_currency == currency and value.total > 0
        }
        current_item_totals = {
            name: value.total
            for (item_currency, name), value in current.items.items()
            if item_currency == currency and value.total > 0
        }
        previous_item_totals = {
            name: value.total
            for (item_currency, name), value in previous.items.items()
            if item_currency == currency and value.total > 0
        }
        current_category_totals = category_totals(current_item_totals)
        previous_category_totals = category_totals(previous_item_totals)
        category_rows = sorted(
            (
                (
                    category,
                    current_category_totals.get(category, 0.0),
                    previous_category_totals.get(category, 0.0),
                )
                for category in set(current_category_totals) | set(previous_category_totals)
            ),
            key=lambda row: row[1],
            reverse=True,
        )
        store_changes = changed_rows(current_store_totals, previous_store_totals, top)
        item_changes = changed_rows(
            {
                name: total
                for name, total in current_item_totals.items()
                if not is_service_item(name)
            },
            {
                name: total
                for name, total in previous_item_totals.items()
                if not is_service_item(name)
            },
            top,
        )

        print_header(f"Короткий вывод ({currency_label})")
        print(insight_line("Чистые расходы", stats.total, previous_stats.total, currency))
        print(insight_line("Среднее в день", avg_day, previous_avg_day, currency))
        print(
            insight_line(
                "Возвраты и отмены",
                stats.refund_total,
                previous_stats.refund_total,
                currency,
            )
        )
        print()

        print_header(f"Сравнение ({currency_label})")
        print_table(
            ("Показатель", "Текущий период", "Прошлый период", "Изменение"),
            (
                (
                    "Чистые расходы",
                    money(stats.total, currency),
                    money(previous_stats.total, currency),
                    colored_expense_delta(stats.total, previous_stats.total, currency),
                ),
                (
                    "Расходы до возвратов",
                    money(stats.gross_total, currency),
                    money(previous_stats.gross_total, currency),
                    colored_expense_delta(
                        stats.gross_total, previous_stats.gross_total, currency
                    ),
                ),
                (
                    "Покупочные чеки",
                    stats.count,
                    previous_stats.count,
                    count_delta(stats.count, previous_stats.count),
                ),
                (
                    "Средний чек",
                    money(avg_receipt, currency),
                    money(previous_avg_receipt, currency),
                    colored_expense_delta(avg_receipt, previous_avg_receipt, currency),
                ),
                (
                    "Среднее в день",
                    money(avg_day, currency),
                    money(previous_avg_day, currency),
                    colored_expense_delta(avg_day, previous_avg_day, currency),
                ),
                (
                    "Возвраты и отмены",
                    COLOR.warning(f"-{money(stats.refund_total, currency)}"),
                    COLOR.warning(f"-{money(previous_stats.refund_total, currency)}"),
                    colored_neutral_money_delta(
                        -stats.refund_total, -previous_stats.refund_total, currency
                    ),
                ),
            ),
        )
        print()

        print_header(f"Категории ({currency_label})")
        print_table(
            ("Категория", "Сейчас", "Было", "Изменение", "Доля"),
            (
                (
                    category,
                    money(total, currency),
                    money(previous_total, currency),
                    colored_expense_delta(total, previous_total, currency),
                    share_bar(total, stats.total),
                )
                for category, total, previous_total in category_rows
                if total > 0 or previous_total > 0
            ),
        )
        print()

        print_header(f"Главные изменения ({currency_label})")
        print(f"Топ-{top} изменений по магазинам")
        print_table(
            ("Магазин", "Сейчас", "Было", "Изменение"),
            (
                (
                    store,
                    money(current_total, currency),
                    money(previous_total, currency),
                    colored_expense_delta(current_total, previous_total, currency),
                )
                for store, current_total, previous_total in store_changes
            ),
        )
        print()

        print(f"Топ-{top} изменений по товарам")
        print_table(
            ("Товар", "Сейчас", "Было", "Изменение"),
            (
                (
                    item,
                    money(current_total, currency),
                    money(previous_total, currency),
                    colored_expense_delta(current_total, previous_total, currency),
                )
                for item, current_total, previous_total in item_changes
            ),
        )
        print()

        print_header(f"Итоги ({currency_label})")
        print_table(
            ("Показатель", "Значение"),
            (
                ("Чистые расходы", money(stats.total, currency)),
                ("Расходы до возвратов", money(stats.gross_total, currency)),
                ("Покупочные чеки", stats.count),
                ("Средний чек", money(avg_receipt, currency)),
                ("Среднее в день", money(avg_day, currency)),
            ),
        )
        print()

        print_header(f"Корректировки ({currency_label})")
        print_table(
            ("Корректировка", "Чеки", "Сумма"),
            (
                (
                    "Возвраты и отмены",
                    stats.refund_count,
                    COLOR.warning(f"-{money(stats.refund_total, currency)}"),
                ),
                (
                    "Закрытие уже учтенной предоплаты",
                    stats.ignored_count,
                    COLOR.warning(money(stats.ignored_total, currency)),
                ),
            ),
        )
        print()

        refund_rows = sorted(
            (
                (store, value.count, value.total)
                for (item_currency, store), value in current.refund_stores.items()
                if item_currency == currency and value.total > 0
            ),
            key=lambda row: row[2],
            reverse=True,
        )[:top]
        if refund_rows:
            print(COLOR.warning(f"Топ-{top} магазинов по возвратам и отменам"))
            print_table(
                ("Магазин", "Чеки", "Сумма"),
                (
                    (store, count, money(total, currency))
                    for store, count, total in refund_rows
                ),
            )
            print()

        print_header(f"Основная разбивка ({currency_label})")

        print(f"Топ-{top} магазинов")
        store_rows = sorted(
            (
                (
                    store,
                    value.count,
                    value.total,
                    previous.stores[(currency, store)].total,
                )
                for (item_currency, store), value in current.stores.items()
                if item_currency == currency and value.total > 0
            ),
            key=lambda row: row[2],
            reverse=True,
        )[:top]
        print_table(
            ("Магазин", "Чеки", "Сейчас", "Было", "Изменение", "Доля"),
            (
                (
                    store,
                    count,
                    money(total, currency),
                    money(previous_total, currency),
                    colored_expense_delta(total, previous_total, currency),
                    share_bar(total, stats.total),
                )
                for store, count, total, previous_total in store_rows
            ),
        )
        print()

        print_header(f"Товары ({currency_label})")

        print(f"Топ-{top} товаров")
        rows = sorted(
            (
                (name, value.quantity, value.total)
                for (item_currency, name), value in current.items.items()
                if item_currency == currency
                and value.total > 0
                and not is_service_item(name)
            ),
            key=lambda row: row[2],
            reverse=True,
        )[:top]
        print_table(
            ("Товар", "Кол-во", "Сейчас", "Было", "Изменение", "Доля"),
            (
                (
                    name,
                    f"{quantity:.3g}",
                    money(total, currency),
                    money(previous.items[(currency, name)].total, currency),
                    colored_expense_delta(
                        total, previous.items[(currency, name)].total, currency
                    ),
                    share_bar(total, stats.total),
                )
                for name, quantity, total in rows
            ),
        )
        print()

        print(f"Топ-{top} сервисов и комиссий")
        service_rows = sorted(
            (
                (name, value.quantity, value.total)
                for (item_currency, name), value in current.items.items()
                if item_currency == currency and value.total > 0 and is_service_item(name)
            ),
            key=lambda row: row[2],
            reverse=True,
        )[:top]
        print_table(
            ("Строка", "Кол-во", "Сейчас", "Было", "Изменение", "Доля"),
            (
                (
                    name,
                    f"{quantity:.3g}",
                    money(total, currency),
                    money(previous.items[(currency, name)].total, currency),
                    colored_expense_delta(
                        total, previous.items[(currency, name)].total, currency
                    ),
                    share_bar(total, stats.total),
                )
                for name, quantity, total in service_rows
            ),
        )
        print()

        print(f"Топ-{top} повторяющихся покупок")
        recurring_rows = sorted(
            (
                (
                    name,
                    len(value.purchase_receipts),
                    value.quantity,
                    value.total,
                    value.total / value.quantity if value.quantity else 0,
                )
                for (item_currency, name), value in current.items.items()
                if item_currency == currency
                and value.total > 0
                and value.quantity > 1
                and len(value.purchase_receipts) > 1
                and not is_service_item(name)
            ),
            key=lambda row: (row[2], row[3]),
            reverse=True,
        )[:top]
        print_table(
            (
                "Товар",
                "Покупки",
                "Кол-во",
                "Сейчас",
                "Было",
                "Изменение",
                "Средняя цена",
            ),
            (
                (
                    name,
                    purchases,
                    f"{quantity:.3g}",
                    money(total, currency),
                    money(previous.items[(currency, name)].total, currency),
                    colored_expense_delta(
                        total, previous.items[(currency, name)].total, currency
                    ),
                    money(avg_unit, currency),
                )
                for name, purchases, quantity, total, avg_unit in recurring_rows
            ),
        )
        print()

        if ai_summary:
            prompt = build_ai_prompt(
                currency_label=currency_label,
                currency=currency,
                days=days,
                current_start=start,
                current_end=end,
                previous_start=previous_start,
                previous_end=previous_end,
                stats=stats,
                previous_stats=previous_stats,
                avg_day=avg_day,
                previous_avg_day=previous_avg_day,
                previous_item_totals=previous_item_totals,
                category_rows=category_rows,
                store_changes=store_changes,
                item_changes=item_changes,
                store_rows=store_rows,
                item_rows=rows,
                service_rows=service_rows,
                recurring_rows=recurring_rows,
            )
            print_ai_summary(prompt, ai_command, ai_timeout)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Построить отчет по покупкам из SQLite-базы LKDR."
    )
    parser.add_argument("--db", default=DEFAULT_DB, type=Path, help="Путь к lkdr.db")
    parser.add_argument(
        "--days",
        default=DEFAULT_DAYS,
        type=int,
        help="Длина текущего периода в днях, предыдущий период будет такой же длины",
    )
    parser.add_argument(
        "--as-of",
        type=parse_datetime,
        help="Дата и время конца отчета в ISO-формате, по умолчанию самый свежий чек",
    )
    parser.add_argument("--top", default=10, type=int, help="Количество строк в топах")
    parser.add_argument(
        "--color",
        choices=("auto", "always", "never"),
        default="auto",
        help="Режим цветного вывода: auto, always или never",
    )
    parser.add_argument(
        "--currency-store",
        action="append",
        default=[],
        type=parse_currency_rule,
        metavar="STORE=CURRENCY",
        help="Принудительно указать валюту для точного названия магазина, например 'Kaspi.kz=KZT'",
    )
    parser.add_argument(
        "--currency-receipt",
        action="append",
        default=[],
        type=parse_currency_rule,
        metavar="RECEIPT_KEY=CURRENCY",
        help="Принудительно указать валюту для конкретного receipt_key",
    )
    parser.add_argument(
        "--ai-summary",
        action="store_true",
        help="Добавить AI-выводы и рекомендации через Codex CLI",
    )
    parser.add_argument(
        "--ai-command",
        default="codex",
        help="Команда Codex CLI для AI-выводов, по умолчанию codex",
    )
    parser.add_argument(
        "--ai-timeout",
        default=180,
        type=int,
        help="Сколько секунд ждать ответ Codex CLI, по умолчанию 180",
    )
    return parser


def main() -> None:
    args = build_parser().parse_args()
    run_report(
        args.db,
        args.days,
        args.as_of,
        args.top,
        args.currency_store,
        args.currency_receipt,
        args.color,
        args.ai_summary,
        args.ai_command,
        args.ai_timeout,
    )


if __name__ == "__main__":
    main()
