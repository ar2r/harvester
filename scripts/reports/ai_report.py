#!/usr/bin/env python3
"""HTML-отчёт «Месяц в чеках» с AI-выводами (по шаблону-прототипу).

Создаёт или обновляет <out-dir>/lkdr-YYYY-MM.html: месяц берётся из конца
отчётного периода (самый свежий чек в базе или --as-of), поэтому в каждом
месяце получается свой файл, а перезапуск команды просто обновляет его.

Расчёты переиспользует scripts/reports/lkdr_report.py (периоды, категории,
валюты). AI-карточки запрашиваются у Codex CLI как JSON; если CLI недоступен
или ответил не по схеме, карточки строятся детерминированно из данных.

Запуск: make ai-report | ./scripts/reports/ai_report.py --db lkdr.db [--no-ai]
"""

from __future__ import annotations

import argparse
import html
import json
import math
import re
import shlex
import shutil
import sqlite3
import subprocess
import sys
from collections import defaultdict
from datetime import datetime, timedelta
from pathlib import Path

import _config
import lkdr_report as base

DEFAULT_TEMPLATE = Path(__file__).resolve().parent.parent / "templates" / "lkdr-report.html"
DEFAULT_OUT_DIR = Path("reports")

MONTHS_RU = {
    1: "Январь", 2: "Февраль", 3: "Март", 4: "Апрель", 5: "Май", 6: "Июнь",
    7: "Июль", 8: "Август", 9: "Сентябрь", 10: "Октябрь", 11: "Ноябрь", 12: "Декабрь",
}

# ---------------------------------------------------------------------------
# Рендеринг шаблона
# ---------------------------------------------------------------------------

BLOCK_RE = re.compile(r"[ \t]*<!-- block:([\w-]+) -->.*?<!-- /block:\1 -->[ \t]*\n?", re.DOTALL)
SCALAR_RE = re.compile(r"\{\{\s*(\w+)\s*\}\}")


def apply_blocks(template: str, blocks: dict[str, str]) -> str:
    def replace(match: re.Match) -> str:
        return blocks.get(match.group(1), match.group(0))

    return BLOCK_RE.sub(replace, template)


def apply_scalars(template: str, values: dict[str, object]) -> str:
    def replace(match: re.Match) -> str:
        key = match.group(1)
        if key not in values:
            return match.group(0)
        return html.escape(str(values[key]), quote=True)

    rendered = SCALAR_RE.sub(replace, template)
    leftovers = sorted(set(SCALAR_RE.findall(rendered)))
    if leftovers:
        raise RuntimeError(f"не заполнены плейсхолдеры шаблона: {leftovers}")

    return rendered


# Подструктуры повторяемых блоков — зеркалируют примеры из шаблона-прототипа.

T_PAIRED = """      <div class="paired">
        <div class="bar-label">
          <strong>{label}</strong>
          <span class="difference">{diff}</span>
        </div>
        <div class="pair-line">
          <span>Раньше</span>
          <div class="pair-track"><div class="previous" style="width:{prev_pct}%"></div></div>
          <b>{prev}</b>
        </div>
        <div class="pair-line">
          <span>Сейчас</span>
          <div class="pair-track"><div class="current" style="width:{cur_pct}%"></div></div>
          <b>{cur}</b>
        </div>
      </div>"""

T_COMPARE_PAIRED = T_PAIRED  # в примере сравнения к подписи добавляется «· общая шкала»

T_COMPARE_ROW = """        <tr>
          <td>{name}</td>
          <td class="num">{prev}</td>
          <td class="num">{cur}</td>
          <td class="num {diff_class}">{diff}</td>
        </tr>"""

T_AI_CARD = """    <article class="ai-card">
      <div class="index">{index} · {theme}</div>
      <h3>{title}</h3>
      <p>{body}</p>
    </article>"""

T_FOOD_ROW = """      <div class="food-row">
        <span>{name}</span>
        <b>{value}<br><small>{share}</small></b>
      </div>"""

T_CATEGORY_VISUAL = """    <div class="category-visual">
      <div class="category-caption">
        <span class="category-rank">{rank}</span>
        <div>
          <h3>{name}</h3>
          <small>{note}</small>
        </div>
      </div>
      <div class="category-bars">
        <div class="category-series">
          <span class="series-label">Раньше</span>
          <div class="signed-track">
            <i class="zero-line" style="left:{zero_pct:.1f}%"></i>
            <i class="signed-bar previous{prev_neg}" style="left:{prev_left:.1f}%;width:{prev_width:.1f}%"></i>
          </div>
          <strong>{prev}</strong>
        </div>
        <div class="category-series">
          <span class="series-label">Сейчас</span>
          <div class="signed-track">
            <i class="zero-line" style="left:{zero_pct:.1f}%"></i>
            <i class="signed-bar current{cur_neg}" style="left:{cur_left:.1f}%;width:{cur_width:.1f}%"></i>
          </div>
          <strong>{cur}</strong>
        </div>
      </div>
      <div class="category-change">
        <strong>{change}</strong>
        <span class="change-chip {chip_class}">{chip}</span>
        <small>{change_note}</small>
      </div>
      <div class="category-share">
        <strong>{share}</strong>
        <small>{share_note}</small>
        <div class="share-track"><span style="width:{share_width:.0f}%"></span></div>
      </div>
    </div>"""

T_STORE_ROW = """          <tr>
            <td>{name}</td>
            <td class="num">{cur}</td>
            <td class="num">{prev}</td>
            <td class="num {diff_class}">{diff}</td>
            <td class="num">{share}</td>
            <td class="mini-comparison" aria-label="Раньше {prev}, сейчас {cur}">
              <span class="mini-pair">
                <span class="mini-track previous-track"><span class="mini-series previous" style="width:{prev_pct}%"></span></span>
                <span class="mini-track current-track"><span class="mini-series current" style="width:{cur_pct}%"></span></span>
              </span>
            </td>
          </tr>"""

T_STORES_HIGHLIGHT = """    <div class="food-row">
      <span>{name}</span>
      <b>{value}</b>
    </div>"""

T_ITEM_ROW = """          <tr>
            <td>{name}</td>
            <td class="num">{receipts}</td>
            <td class="num">{qty}</td>
            <td class="num">{total}</td>
          </tr>"""

T_ACTION_STEP = """      <li><strong>{head}</strong> {body}</li>"""

T_METHOD_RULE = """        <li>{rule}</li>"""


# ---------------------------------------------------------------------------
# Форматирование
# ---------------------------------------------------------------------------

def fmt_int(value: int) -> str:
    return f"{value:,}".replace(",", " ")


def fmt_delta(current: float, previous: float, currency: str, with_pct: bool = True) -> str:
    diff = current - previous
    sign = "+" if diff > 0 else ("−" if diff < 0 else "±")
    pct = f" ({diff / previous * 100:+.1f}%)" if with_pct and previous else ""
    return f"{sign}{base.money(abs(diff), currency)}{pct}"


def fmt_share(value: float, total: float) -> str:
    return f"{value / total * 100:.1f}%" if total else "0%"


def pair_widths(previous: float, current: float) -> tuple[int, int]:
    scale = max(abs(previous), abs(current), 1e-9)
    return round(abs(previous) / scale * 100), round(abs(current) / scale * 100)


def nice_axis_max(value: float) -> float:
    if value <= 0:
        return 100.0

    step = 10 ** math.floor(math.log10(value))
    for multiplier in (1, 2, 5, 10):
        if value <= multiplier * step:
            return multiplier * step

    return 10 * step


def cumulative_series(report: base.PeriodReport, currency: str, start: datetime, days: int) -> list[float]:
    per_day: dict[str, float] = defaultdict(float)
    for (item_currency, day), stats in report.days_total.items():
        if item_currency == currency:
            per_day[day] += stats.total

    series, running = [], 0.0
    for index in range(days):
        day = (start + timedelta(days=index)).strftime("%Y-%m-%d")
        running += per_day.get(day, 0.0)
        series.append(running)

    return series


def polyline(series: list[float], axis_max: float) -> str:
    points = []
    count = len(series)
    for index, value in enumerate(series):
        x = 75 + (962 - 75) * (index / (count - 1) if count > 1 else 0)
        y = 270 - (270 - 35) * min(value / axis_max, 1.0)
        points.append(f"{x:.1f},{y:.1f}")

    return " ".join(points)


def signed_geometry(rows: list[tuple[str, float, float]]) -> tuple[float, object]:
    values = [value for _, current, previous in rows for value in (current, previous)]
    vmax = max(values + [0.0])
    vmin = min(values + [0.0])
    span = vmax - vmin or 1.0
    zero = -vmin / span * 100

    def geometry(value: float) -> tuple[float, float, str]:
        width = abs(value) / span * 100
        if value >= 0:
            return zero, width, ""
        return zero - width, width, " negative-bar"

    return zero, geometry


# ---------------------------------------------------------------------------
# Данные
# ---------------------------------------------------------------------------

def collect(
    conn: sqlite3.Connection,
    args: argparse.Namespace,
    private_categories: list[str] | None = None,
) -> dict:
    end = args.as_of or base.latest_receipt_datetime(conn)
    start = end - timedelta(days=args.days)
    previous_end = start
    previous_start = previous_end - timedelta(days=args.days)

    store_rules = base.build_rule_map([])
    receipt_rules = base.build_rule_map([])
    current = base.build_period_report(conn, start, end, store_rules, receipt_rules)
    previous = base.build_period_report(conn, previous_start, previous_end, store_rules, receipt_rules)

    currency = pick_currency(current, previous, args.currency)
    stats = current.stats_by_currency[currency]
    previous_stats = previous.stats_by_currency[currency]

    def totals(report: base.PeriodReport) -> tuple[dict[str, float], dict[str, float], dict[str, int]]:
        stores = {
            store: value.total
            for (item_currency, store), value in report.stores.items()
            if item_currency == currency and value.total > 0
        }
        items = {
            name: value.total
            for (item_currency, name), value in report.items.items()
            if item_currency == currency and value.total > 0
        }
        store_counts = {
            store: value.count
            for (item_currency, store), value in report.stores.items()
            if item_currency == currency and value.total > 0
        }
        return stores, items, store_counts

    current_stores, current_items, current_store_counts = totals(current)
    previous_stores, previous_items, _ = totals(previous)

    category_current = dict(base.category_totals(current_items))
    category_previous = dict(base.category_totals(previous_items))
    category_rows = sorted(
        (
            (category, category_current.get(category, 0.0), category_previous.get(category, 0.0))
            for category in set(category_current) | set(category_previous)
        ),
        key=lambda row: row[1],
        reverse=True,
    )

    # Товары приватных категорий (по умолчанию аптечка/врачи/анализы) не
    # выводятся построчно: суммы остаются только на уровне категории.
    private = set(
        private_categories
        if private_categories is not None
        else _config.DEFAULT_PRIVATE_CATEGORIES
    )

    def hidden_item(name: str) -> bool:
        return base.categorize_item(name) in private

    recurring = []
    for (item_currency, name), item in current.items.items():
        if item_currency != currency or item.total <= 0:
            continue
        purchases = len(item.purchase_receipts)
        if (
            purchases > 1
            and item.quantity > 1
            and not base.is_service_item(name)
            and not hidden_item(name)
        ):
            recurring.append((name, purchases, item.quantity, item.total, item.total / item.quantity))
    recurring.sort(key=lambda row: row[3], reverse=True)

    top_items = sorted(
        (
            (name, total)
            for name, total in current_items.items()
            if not hidden_item(name)
        ),
        key=lambda pair: pair[1],
        reverse=True,
    )[: args.top]

    # Разбор Прочего: что осталось без категории — топ позиций и хвост.
    other_rows = sorted(
        (
            (name, item.quantity, item.total)
            for (item_currency, name), item in current.items.items()
            if item_currency == currency
            and item.total > 0
            and base.categorize_item(name) == "Прочее"
        ),
        key=lambda row: row[2],
        reverse=True,
    )
    other_top = other_rows[: args.top]
    other_total = sum(total for _, _, total in other_rows)

    # Корзина продуктов на неделю: отдельное окно, частота — по срокам
    # годности. Считается по основной валюте отчёта.
    basket: dict[str, list[base.BasketEntry]] = {}
    basket_weekly_total = 0.0
    basket_days = getattr(args, "basket_days", 180)
    if basket_days >= 7:
        basket_report = base.build_period_report(
            conn, end - timedelta(days=basket_days), end, store_rules, receipt_rules
        )
        baskets = base.build_weekly_basket(basket_report, basket_days, args.top)
        for bucket, entries in (baskets.get(currency) or {}).items():
            basket[bucket] = entries
            basket_weekly_total += sum(entry.weekly_sum for entry in entries)

    return {
        "end": end,
        "start": start,
        "previous_start": previous_start,
        "previous_end": previous_end,
        "days": args.days,
        "top": args.top,
        "currency": currency,
        "stats": stats,
        "previous_stats": previous_stats,
        "current_stores": current_stores,
        "previous_stores": previous_stores,
        "current_store_counts": current_store_counts,
        "current_items": current_items,
        "previous_items": previous_items,
        "category_rows": category_rows,
        "recurring": recurring,
        "top_items": top_items,
        "other_rows": other_top,
        "other_rest_count": max(len(other_rows) - len(other_top), 0),
        "other_rest_total": sum(total for _, _, total in other_rows[args.top :]),
        "other_total": other_total,
        "basket": basket,
        "basket_days": basket_days,
        "basket_weekly_total": basket_weekly_total,
        "current_report": current,
        "previous_report": previous,
        "last_receipt": base.latest_receipt_datetime(conn),
    }


def pick_currency(current: base.PeriodReport, previous: base.PeriodReport, preferred: str) -> str:
    if preferred != "auto":
        return preferred

    totals: dict[str, float] = defaultdict(float)
    for report in (current, previous):
        for currency, stats in report.stats_by_currency.items():
            totals[currency] += abs(stats.total)

    return max(totals, key=totals.get) if totals else "RUB"


# ---------------------------------------------------------------------------
# AI-выводы
# ---------------------------------------------------------------------------

def build_ai_json_prompt(data: dict, max_item_name_chars: int = _config.DEFAULT_MAX_ITEM_NAME_CHARS) -> str:
    stats = data["stats"]
    previous_stats = data["previous_stats"]
    currency = data["currency"]

    def lines(title: str, rows: list[str]) -> str:
        body = "\n".join(f"- {row}" for row in rows)
        return f"{title}:\n{body or '- нет данных'}"

    def short_item(name: str) -> str:
        return base.truncate_cell(name, max_item_name_chars)

    category_lines = [
        f"{name}: сейчас {base.money(cur, currency)}, было {base.money(prev, currency)}"
        for name, cur, prev in data["category_rows"][:8]
    ]
    store_change_lines = [
        f"{name}: {fmt_delta(cur, prev, currency)}"
        for name in sorted(
            set(data["current_stores"]) | set(data["previous_stores"]),
            key=lambda name: abs(data["current_stores"].get(name, 0) - data["previous_stores"].get(name, 0)),
            reverse=True,
        )[:5]
        for cur, prev in ((data["current_stores"].get(name, 0), data["previous_stores"].get(name, 0)),)
    ]
    item_change_lines = [
        f"{short_item(name)}: {fmt_delta(cur, prev, currency)}"
        for name, cur in data["top_items"][:5]
        for prev in (data["previous_items"].get(name, 0),)
    ]
    recurring_lines = [
        f"{short_item(name)}: покупок {purchases}, сумма {base.money(total, currency)}, средняя цена {base.money(avg_unit, currency)}"
        for name, purchases, quantity, total, avg_unit in data["recurring"][:5]
    ]

    return f"""
Ты финансовый помощник. Проанализируй расходы семьи из 2 взрослых и 2 подростков
(обычно питаются дома). Отчёт построен по чекам ФНС: возвраты, отмены и дубли
закрытия предоплаты уже учтены. Используй только данные ниже, ничего не выдумывай.

Ответь СТРОГО одним валидным JSON-объектом без markdown-разметки по схеме:
{{
  "lead": "главный вывод месяца, 1-2 предложения (до 240 символов)",
  "cards": [
    {{"theme": "тема в 1-2 словах", "title": "заголовок карточки", "body": "2-3 предложения анализа"}}
  ],
  "actions": [
    {{"head": "что сделать", "body": "конкретное действие по данным"}}
  ]
}}
Карточек 3-5 (разные темы: структура расходов, главные изменения, привычки, что проверить),
действий 2-4. Пиши по-русски, без markdown внутри строк.

Валюта: {currency}
Текущий период: {data['start']:%Y-%m-%d} - {data['end']:%Y-%m-%d} ({data['days']} дней)
Предыдущий период: {data['previous_start']:%Y-%m-%d} - {data['previous_end']:%Y-%m-%d}
Итоги: чистые расходы {fmt_delta(stats.total, previous_stats.total, currency)};
расходы до возвратов {fmt_delta(stats.gross_total, previous_stats.gross_total, currency)};
чеков {stats.count} против {previous_stats.count}; возвраты {base.money(stats.refund_total, currency)}.

{lines("Категории", category_lines)}

{lines("Главные изменения по магазинам", store_change_lines)}

{lines("Топ товаров и их динамика", item_change_lines)}

{lines("Повторяющиеся покупки", recurring_lines)}
""".strip()


def request_ai(prompt: str, command: str, timeout: int) -> tuple[dict | None, str | None]:
    argv = split_command(command)
    if argv is None:
        return None, f"пустая команда AI CLI: {command!r}"

    executable = shutil.which(argv[0])
    if executable is None:
        return None, f"AI CLI не найден: {argv[0]}"

    try:
        result = subprocess.run(
            [executable, *argv[1:]],
            input=prompt,
            text=True,
            capture_output=True,
            timeout=timeout,
            check=False,
        )
    except subprocess.TimeoutExpired:
        return None, f"AI CLI не ответил за {timeout} секунд"

    if result.returncode != 0:
        message = result.stderr.strip() or result.stdout.strip() or "неизвестная ошибка"
        return None, f"AI CLI завершился с ошибкой: {message}"

    text = re.sub(r"^```(?:json)?\s*|\s*```$", "", result.stdout.strip(), flags=re.MULTILINE).strip()
    start, end = text.find("{"), text.rfind("}")
    if start < 0 or end <= start:
        return None, "AI CLI вернул ответ без JSON"

    try:
        payload = json.loads(text[start : end + 1])
    except json.JSONDecodeError as error:
        return None, f"AI CLI вернул невалидный JSON: {error}"

    if not isinstance(payload.get("lead"), str) or not isinstance(payload.get("cards"), list) or not payload["cards"]:
        return None, "JSON AI CLI без lead/cards"

    return payload, None


def split_command(command: str) -> list[str] | None:
    """Командная строка агента → argv; None, если она пуста или битая."""
    try:
        argv = shlex.split(command)
    except ValueError:
        return None

    return argv or None


def fallback_ai(data: dict, max_item_name_chars: int = _config.DEFAULT_MAX_ITEM_NAME_CHARS) -> dict:
    stats = data["stats"]
    previous_stats = data["previous_stats"]
    currency = data["currency"]

    def short_item(name: str) -> str:
        return base.truncate_cell(name, max_item_name_chars)

    lead = (
        f"Чистые расходы за {data['days']} дней — {base.money(stats.total, currency)} "
        f"против {base.money(previous_stats.total, currency)} раньше "
        f"({fmt_delta(stats.total, previous_stats.total, currency)})."
    )

    cards = []
    if data["category_rows"]:
        name, current, previous = data["category_rows"][0]
        share = fmt_share(current, stats.total) if stats.total else "0%"
        cards.append((
            "Структура",
            f"Крупнейшая категория — {name}",
            f"{base.money(current, currency)} ({share} расходов) против "
            f"{base.money(previous, currency)} в прошлом периоде "
            f"({fmt_delta(current, previous, currency)}).",
        ))

    changes = [
        (name, current - previous, current, previous)
        for name, current, previous in data["category_rows"]
    ]
    increases = sorted(changes, key=lambda row: row[1], reverse=True)
    if increases and increases[0][1] > 0:
        name, diff, current, previous = increases[0]
        cards.append((
            "Рост",
            f"Сильнее всего выросло: {name}",
            f"+{base.money(diff, currency)} к прошлому периоду "
            f"({base.money(previous, currency)} → {base.money(current, currency)}).",
        ))
    decreases = sorted(changes, key=lambda row: row[1])
    if decreases and decreases[0][1] < 0:
        name, diff, current, previous = decreases[0]
        cards.append((
            "Экономия",
            f"Сильнее всего снизилось: {name}",
            f"{base.money(diff, currency)} к прошлому периоду "
            f"({base.money(previous, currency)} → {base.money(current, currency)}).",
        ))
    if data["recurring"]:
        name, purchases, quantity, total, avg_unit = data["recurring"][0]
        cards.append((
            "Привычки",
            f"Регулярная покупка: {short_item(name)}",
            f"{purchases} покупок на {base.money(total, currency)} "
            f"(средняя цена {base.money(avg_unit, currency)}).",
        ))
    if len(cards) < 2:
        cards.append((
            "Данных мало",
            "Недостаточно чеков для выводов",
            "Накопите больше данных и пересоберите отчёт.",
        ))

    actions = [
        ("Сверить крупные чеки", "Проверьте вручную самые дорогие покупки периода в текстовом отчёте (make lkdr-report)."),
        ("Обновить данные", "Запустите make parse перед пересборкой отчёта, чтобы подтянуть свежие чеки."),
    ]

    return {"lead": lead, "cards": cards, "actions": actions}


# ---------------------------------------------------------------------------
# Сборка HTML
# ---------------------------------------------------------------------------

def build_render(
    data: dict,
    ai: dict,
    max_item_name_chars: int = _config.DEFAULT_MAX_ITEM_NAME_CHARS,
) -> tuple[dict[str, object], dict[str, str]]:
    currency = data["currency"]
    symbol = base.CURRENCY_SYMBOLS.get(currency, currency)
    stats = data["stats"]
    previous_stats = data["previous_stats"]
    days = data["days"]
    end, start = data["end"], data["start"]

    def short_item(name: str) -> str:
        return base.truncate_cell(name, max_item_name_chars)

    avg_day = stats.total / days
    previous_avg_day = previous_stats.total / days
    avg_check = stats.total / stats.count if stats.count else 0
    previous_avg_check = previous_stats.total / previous_stats.count if previous_stats.count else 0

    period_range = f"{start:%d.%m.%Y} – {end:%d.%m.%Y}"
    previous_range = f"{data['previous_start']:%d.%m.%Y} – {data['previous_end']:%d.%m.%Y}"
    tz = datetime.now().astimezone().tzname() or ""

    scalars: dict[str, object] = {
        "brand_name": "LedgerFox",
        "generated_at": f"{datetime.now():%d.%m.%Y %H:%M}",
        "currency": f"{currency} ({symbol})",
        "title": "Месяц в чеках",
        "period_current": period_range,
        "period_previous": previous_range,
        "period_days": str(days),
        "boundary_time": f"{start:%d.%m.%Y %H:%M}",
        "boundary_tz": tz,
        "pill_text": f"{MONTHS_RU[end.month]} {end.year} · {currency}",
        "as_of_time": f"{end:%d.%m.%Y %H:%M}",
        "last_receipt_time": f"{data['last_receipt']:%d.%m.%Y %H:%M}",
        # Метрики
        "metric_1_label": "Чистые расходы",
        "metric_1_value": base.money(stats.total, currency),
        "metric_1_note": f"{fmt_delta(stats.total, previous_stats.total, currency)} к прошлым {days} дням",
        "metric_2_label": "Расходы до возвратов",
        "metric_2_value": base.money(stats.gross_total, currency),
        "metric_2_note": f"было {base.money(previous_stats.gross_total, currency)}",
        "metric_3_label": "Покупочные чеки",
        "metric_3_value": fmt_int(stats.count),
        "metric_3_note": f"было {fmt_int(previous_stats.count)} ({stats.count - previous_stats.count:+d})",
        "metric_4_label": "Среднее в день",
        "metric_4_value": base.money(avg_day, currency),
        "metric_4_note": f"было {base.money(previous_avg_day, currency)}",
        # Примечание о полноте
        "notice_head": "Полнота данных:",
        "notice_body": (
            f"отчёт по всем чекам базы до последнего ({data['last_receipt']:%d.%m.%Y %H:%M}); "
            "свежие чеки подтянет make parse"
            if data.get("as_of_auto")
            else f"конец периода зафиксирован аргументом --as-of ({end:%d.%m.%Y %H:%M})"
        ),
        # Сравнение
        "period_previous_total": base.money(previous_stats.total, currency),
        "period_previous_note": f"{fmt_int(previous_stats.count)} чеков · {base.money(previous_avg_day, currency)} в день",
        "period_current_total": base.money(stats.total, currency),
        "period_current_note": f"{fmt_int(stats.count)} чеков · {base.money(avg_day, currency)} в день",
        "change_banner_text": f"Чистые расходы за {days} дней: было → стало",
        "change_banner_value": fmt_delta(stats.total, previous_stats.total, currency),
        # AI-секция
        "ai_section_title": "AI-анализ месяца",
        "ai_section_note": f"источник: {data.get('ai_source', 'Codex CLI')}",
        "ai_lead": ai["lead"],
        "ai_footer_note": "Выводы построены только по агрегатам чеков; доходы и цели не оцениваются.",
        # Структура
        "structure_note": "Категории — по товарным позициям чеков",
        "categories_panel_footnote": "Топ-категории, парные полосы в одной шкале на категорию",
        "second_panel_title": "Топ позиций в чеках",
        "category_list_title": "Все категории: было → стало",
        "category_list_intro": "Полоса «Раньше» и «Сейчас» в общей шкале; отрицательные значения — левее нулевой линии.",
        "category_list_footnote": "Сервисные строки (доставка, упаковка, компенсации) исключены из категорий.",
        # Разбор Прочего
        "other_breakdown_title": "Что осталось в Прочем",
        "other_breakdown_note": (
            f"{base.money(stats.total, currency)} чистых расходов; "
            f"Прочее — {base.money(data['other_total'], currency)}"
        ),
        "other_breakdown_footnote": (
            f"Позиции, не распознанные категориями; "
            f"ещё {data['other_rest_count']} позиций на {base.money(data['other_rest_total'], currency)} "
            "не показаны. Пустые названия чеков отмечены как «(без названия)»."
        ),
        "category_total_title": "Сумма по категориям",
        "category_total_previous_label": "Предыдущий период:",
        "category_total_current_label": "Текущий период:",
        "category_total_diff_label": "Изменение:",
        "details_summary": "Полные списки категорий обоих периодов",
        # График
        "daily_note": "Накопленные чистые расходы по дням",
        "chart_title": f"Накопленные расходы, {days} дней",
        "chart_desc": f"Кумулятивные чистые расходы за последние и предыдущие {days} дней",
        "chart_footnote": "Возвраты вычитаются в день чека; дни без покупок продолжают накопленную сумму.",
        "x_first_label": "День 1",
        "x_last_label": f"День {days}",
        # Магазины
        "stores_note": f"Топ-{data['top']} магазинов по чистым расходам",
        "mini_legend_note": "полосы — доли от максимума колонки «Сейчас»",
        "stores_highlight_title": "Главные изменения по магазинам",
        # Привычки
        "habits_note": "Позиции, встретившиеся более чем в одном чеке",
        # Корзина на неделю
        "basket_title": "Корзина продуктов на неделю",
        "basket_note": (
            f"регулярные покупки за {data['basket_days']} дней; "
            f"ориентир трат: {base.money(data['basket_weekly_total'], currency)}/нед"
        ),
        "basket_footer": (
            "Количество — средний расход в неделю за окно корзины; частота закупок — "
            "по типовому сроку годности: скоропортящееся каждую неделю, "
            "длительного хранения — раз в 2-4 недели или запасом."
        ),
        # Действия
        "actions_title": "Что сделать в следующем месяце",
        # Методика
        "method_note": "Все суммы — чистые: с учётом возвратов и без дублей закрытия предоплаты",
        "check_1_label": "Покупочные чеки, сумма",
        "check_2_label": "Возвраты и отмены",
        "check_3_label": "Закрытия предоплаты (пропущены)",
        "prev_check_1_label": "Покупочные чеки, сумма",
        "prev_check_2_label": "Возвраты и отмены",
        "prev_check_3_label": "Закрытия предоплаты (пропущены)",
        "current_period_footnote": "Расхождение контрольных сумм:",
        "previous_period_footnote": "Механика корректировок идентична текущему периоду.",
        "method_details_summary": "Правила расчёта",
        # Подвал
        "footer_note": (
            "Отчёт сформирован LedgerFox из локальной базы чеков ФНС; AI-выводы — "
            f"{data.get('ai_source', 'Codex CLI')}. Файл месяца обновляется повторным запуском make ai-report."
        ),
    }

    # Контрольные суммы
    def checks(s: base.MutableStats) -> tuple[str, str, str, str]:
        discrepancy = s.gross_total - s.total - s.refund_total
        return (
            base.money(s.gross_total, currency),
            base.money(s.refund_total, currency),
            f"{fmt_int(s.ignored_count)} чеков · {base.money(s.ignored_total, currency)}",
            base.money(discrepancy, currency),
        )

    scalars["check_1_value"], scalars["check_2_value"], scalars["check_3_value"], scalars["check_discrepancy"] = checks(stats)
    scalars["prev_check_1_value"], scalars["prev_check_2_value"], scalars["prev_check_3_value"] = checks(previous_stats)[:3]

    scalars["category_total_previous_value"] = base.money(sum(prev for _, _, prev in data["category_rows"]), currency)
    scalars["category_total_current_value"] = base.money(sum(cur for _, cur, _ in data["category_rows"]), currency)
    scalars["category_total_diff_value"] = fmt_delta(
        sum(cur for _, cur, _ in data["category_rows"]),
        sum(prev for _, _, prev in data["category_rows"]),
        currency,
    )

    scalars["big_number_value"] = (
        base.money(data["top_items"][0][1], currency) if data["top_items"] else base.money(0, currency)
    )

    # График
    cum_current = cumulative_series(data["current_report"], currency, start, days)
    cum_previous = cumulative_series(data["previous_report"], currency, data["previous_start"], days)
    axis_max = nice_axis_max(max(cum_current + cum_previous + [0.0]))
    scalars["axis_max_label"] = base.money(axis_max, currency)
    scalars["axis_mid_label"] = base.money(axis_max / 2, currency)
    scalars["series_current_points"] = polyline(cum_current, axis_max)
    scalars["series_previous_points"] = polyline(cum_previous, axis_max)

    # Повторяемые блоки
    blocks: dict[str, str] = {"chart-empty-zone": ""}

    compare_metrics = [
        ("Чистые расходы", previous_stats.total, stats.total, "money"),
        ("Расходы до возвратов", previous_stats.gross_total, stats.gross_total, "money"),
        ("Покупочные чеки", previous_stats.count, stats.count, "count"),
        ("Средний чек", previous_avg_check, avg_check, "money"),
        ("Среднее в день", previous_avg_day, avg_day, "money"),
        ("Возвраты и отмены", previous_stats.refund_total, stats.refund_total, "money"),
    ]

    def fmt_value(value: float, kind: str) -> str:
        return fmt_int(int(value)) if kind == "count" else base.money(value, currency)

    paired_rows = []
    table_rows = []
    for name, previous, current, kind in compare_metrics:
        if kind != "count":
            prev_pct, cur_pct = pair_widths(previous, current)
            paired_rows.append(
                T_PAIRED.format(
                    label=f"{name} · общая шкала",
                    diff=fmt_delta(current, previous, currency),
                    prev=fmt_value(previous, kind),
                    cur=fmt_value(current, kind),
                    prev_pct=prev_pct,
                    cur_pct=cur_pct,
                )
            )

        diff_class = "increase" if current > previous else ("decrease" if current < previous else "")
        table_rows.append(
            T_COMPARE_ROW.format(
                name=name,
                prev=fmt_value(previous, kind),
                cur=fmt_value(current, kind),
                diff_class=diff_class,
                diff=fmt_delta(current, previous, currency),
            )
        )

    blocks["compare-paired"] = "\n".join(paired_rows)
    blocks["compare-row"] = "\n".join(table_rows)

    cards = []
    for index, card in enumerate(ai["cards"][:6], start=1):
        theme, title, body = (card[0], card[1], card[2]) if isinstance(card, (list, tuple)) else (
            card.get("theme", "Тема"), card.get("title", ""), card.get("body", "")
        )
        cards.append(T_AI_CARD.format(index=f"{index:02d}", theme=theme, title=title, body=body))
    blocks["ai-cards"] = "\n".join(cards)

    blocks["category-paired"] = "\n".join(
        T_PAIRED.format(
            label=name,
            diff=fmt_delta(current, previous, currency),
            prev=base.money(previous, currency),
            cur=base.money(current, currency),
            prev_pct=prev_pct,
            cur_pct=cur_pct,
        )
        for name, current, previous in data["category_rows"][:5]
        for prev_pct, cur_pct in (pair_widths(previous, current),)
    )

    blocks["food-row"] = "\n".join(
        T_FOOD_ROW.format(
            name=short_item(name),
            value=base.money(total, currency),
            share=f"доля {fmt_share(total, stats.total)}",
        )
        for name, total in data["top_items"][:5]
    )

    zero, geometry = signed_geometry(data["category_rows"])
    category_visuals = []
    for rank, (name, current, previous) in enumerate(data["category_rows"], start=1):
        prev_left, prev_width, prev_neg = geometry(previous)
        cur_left, cur_width, cur_neg = geometry(current)
        chip_class = "up" if current > previous else ("down" if current < previous else "")
        chip = "рост" if current > previous else ("снижение" if current < previous else "без изменений")
        category_visuals.append(
            T_CATEGORY_VISUAL.format(
                rank=f"{rank:02d}",
                name=name,
                note=f"топ-позиции категории учтены по товарам чеков",
                zero_pct=zero,
                prev=base.money(previous, currency),
                cur=base.money(current, currency),
                prev_left=prev_left, prev_width=prev_width, prev_neg=prev_neg,
                cur_left=cur_left, cur_width=cur_width, cur_neg=cur_neg,
                change=fmt_delta(current, previous, currency),
                chip_class=chip_class,
                chip=chip,
                change_note="изменение к прошлому периоду",
                share=fmt_share(current, stats.total),
                share_note="доля в чистых расходах",
                share_width=min(current / stats.total * 100, 100) if stats.total else 0,
            )
        )
    blocks["category-visual"] = "\n".join(category_visuals)

    top_stores = sorted(data["current_stores"].items(), key=lambda pair: pair[1], reverse=True)[: data["top"]]
    scale = max((total for _, total in top_stores), default=1.0) or 1.0
    store_rows = []
    for name, total in top_stores:
        previous = data["previous_stores"].get(name, 0.0)
        diff_class = "increase" if total > previous else ("decrease" if total < previous else "")
        cur_pct = round(total / scale * 100)
        prev_pct = round(min(previous, scale) / scale * 100)
        store_rows.append(
            T_STORE_ROW.format(
                name=name,
                cur=base.money(total, currency),
                prev=base.money(previous, currency),
                diff_class=diff_class,
                diff=fmt_delta(total, previous, currency),
                share=fmt_share(total, stats.total),
                prev_pct=prev_pct,
                cur_pct=cur_pct,
            )
        )
    blocks["store-row"] = "\n".join(store_rows) or T_STORE_ROW.format(
        name="нет данных", cur="—", prev="—", diff_class="", diff="—", share="—", prev_pct=0, cur_pct=0
    )

    store_changes = sorted(
        (
            (name, data["current_stores"].get(name, 0.0) - data["previous_stores"].get(name, 0.0))
            for name in set(data["current_stores"]) | set(data["previous_stores"])
        ),
        key=lambda row: abs(row[1]),
        reverse=True,
    )[:3]
    blocks["stores-highlight"] = "\n".join(
        T_STORES_HIGHLIGHT.format(name=name, value=f"{diff:+,.0f}".replace(",", " ") + f" {symbol}")
        for name, diff in store_changes
    ) or T_STORES_HIGHLIGHT.format(name="Изменений нет", value=f"0 {symbol}")

    blocks["item-row"] = "\n".join(
        T_ITEM_ROW.format(
            name=short_item(name),
            receipts=purchases,
            qty=f"{quantity:g}",
            total=base.money(total, currency),
        )
        for name, purchases, quantity, total, avg_unit in data["recurring"][: data["top"]]
    ) or T_ITEM_ROW.format(name="нет повторяющихся покупок", receipts="—", qty="—", total="—")

    T_OTHER_ROW = """          <tr>
            <td>{name}</td>
            <td class="num">{qty}</td>
            <td class="num">{total}</td>
            <td class="num">{share}</td>
          </tr>"""
    other_rows = []
    for name, quantity, total in data["other_rows"]:
        display = short_item(name) if name.strip() else "(без названия)"
        other_rows.append(
            T_OTHER_ROW.format(
                name=display,
                qty=f"{quantity:g}",
                total=base.money(total, currency),
                share=fmt_share(total, data["other_total"]),
            )
        )
    if data["other_rest_count"]:
        other_rows.append(
            T_OTHER_ROW.format(
                name=f"… ещё {data['other_rest_count']} позиций",
                qty="—",
                total=base.money(data["other_rest_total"], currency),
                share=fmt_share(data["other_rest_total"], data["other_total"]),
            )
        )
    blocks["other-row"] = "\n".join(other_rows) or T_OTHER_ROW.format(
        name="ничего не осталось — все позиции распределены по категориям",
        qty="—",
        total=base.money(0, currency),
        share="—",
    )

    T_BASKET_ROW = """          <tr>
            <td>{name}</td>
            <td>{bucket}</td>
            <td class="num">{qty}</td>
            <td class="num">{total}</td>
            <td class="num">{shelf}</td>
          </tr>"""
    basket_rows = []
    for bucket in base.BUCKET_ORDER:
        for entry in data["basket"].get(bucket, []):
            basket_rows.append(
                T_BASKET_ROW.format(
                    name=short_item(entry.name),
                    bucket=bucket,
                    qty=f"{entry.weekly_qty:.4g}",
                    total=base.money(entry.weekly_sum, currency),
                    shelf=f"~{entry.shelf_days} дн.",
                )
            )
    blocks["basket-row"] = "\n".join(basket_rows) or T_BASKET_ROW.format(
        name="регулярных продуктовых покупок не найдено",
        bucket="—",
        qty="—",
        total=base.money(0, currency),
        shelf="—",
    )

    steps = []
    for action in ai.get("actions", [])[:5]:
        if isinstance(action, (list, tuple)):
            head, body = action[0], action[1]
        else:
            head, body = action.get("head", ""), action.get("body", "")
        steps.append(T_ACTION_STEP.format(head=head, body=body))
    blocks["action-step"] = "\n".join(steps)

    blocks["method-rule"] = "\n".join(
        T_METHOD_RULE.format(rule=rule)
        for rule in (
            "Эффективный расход чека — max(total_sum − prepaid_sum, 0): закрытия уже учтённой предоплаты не увеличивают расходы.",
            "operation_type 2 и 3 — отрицательные операции: возвраты и компенсации вычитаются из расходов на полный total_sum.",
            "Категории назначаются по подстроке в названии товара (первое совпадение), без совпадения — «Прочее».",
            "Валюты не складываются: RUB и KZT считаются раздельно, отчёт строится по основной валюте периода.",
            "Товарные суммы и количества берутся со знаком операции; товарная разбивка может расходиться с итогом чеков.",
        )
    )

    return scalars, blocks


# ---------------------------------------------------------------------------
# Точка входа
# ---------------------------------------------------------------------------

def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="HTML-отчёт «Месяц в чеках»: reports/lkdr-YYYY-MM.html")
    parser.add_argument("--db", default="lkdr.db", type=Path, help="Путь к lkdr.db")
    parser.add_argument("--days", default=30, type=int, help="Длина периода сравнения в днях")
    parser.add_argument("--as-of", type=base.parse_datetime, help="Конец отчётного периода (ISO); по умолчанию самый свежий чек")
    parser.add_argument("--top", default=10, type=int, help="Строк в топах")
    parser.add_argument(
        "--basket-days",
        default=180,
        type=int,
        help="Окно корзины продуктов, дней (0 отключает секцию корзины)",
    )
    parser.add_argument("--out-dir", default=DEFAULT_OUT_DIR, type=Path, help="Каталог отчётов (по умолчанию reports)")
    parser.add_argument("--currency", default="auto", help="RUB/KZT или auto — основная валюта периода")
    parser.add_argument("--no-ai", action="store_true", help="Не вызывать AI CLI: карточки из данных")
    parser.add_argument("--ai-command", default=None, help="Команда AI CLI с аргументами (промпт — на stdin); по умолчанию ai.command из config.json, иначе codex")
    parser.add_argument("--ai-timeout", default=180, type=int, help="Таймаут AI CLI, секунды")
    parser.add_argument("--config", default="config.json", type=Path, help="config.json с настройками отчётов (ai.command)")
    parser.add_argument("--template", default=DEFAULT_TEMPLATE, type=Path, help="Файл HTML-шаблона")
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    if args.days < 1:
        raise SystemExit("--days must be at least 1")
    if args.top < 1:
        raise SystemExit("--top must be at least 1")

    if not args.db.exists():
        print(f"База данных не найдена: {args.db}", file=sys.stderr)
        return 1

    try:
        template = args.template.read_text(encoding="utf-8")
    except OSError as error:
        print(f"Шаблон не читается: {error}", file=sys.stderr)
        return 1

    try:
        max_item_name_chars = _config.load_max_item_name_chars(args.config)
        private_categories = _config.load_private_categories(args.config)
    except ValueError as error:
        print(f"{args.config}: {error}", file=sys.stderr)
        return 1

    conn = sqlite3.connect(f"file:{args.db}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    try:
        base.require_tables(conn)
        data = collect(conn, args, private_categories)
    finally:
        conn.close()
    data["as_of_auto"] = args.as_of is None

    ai_payload, ai_error = (None, None)
    ai_command = args.ai_command or _config.load_ai_command(args.config)

    if not args.no_ai:
        ai_payload, ai_error = request_ai(
            build_ai_json_prompt(data, max_item_name_chars), ai_command, args.ai_timeout
        )
        if ai_error:
            print(f"AI недоступен ({ai_error}); карточки построены из данных", file=sys.stderr)

    if ai_payload is None:
        ai_payload = fallback_ai(data, max_item_name_chars)
        data["ai_source"] = "детерминированный анализ данных"
    else:
        agent = (split_command(ai_command) or [ai_command])[0]
        data["ai_source"] = f"AI CLI ({agent})"

    scalars, blocks = build_render(data, ai_payload, max_item_name_chars)

    try:
        rendered = apply_blocks(template, blocks)
        rendered = re.sub(r"<!--.*?-->", "", rendered, flags=re.DOTALL)
        # `-->` вне комментария в HTML быть не может: его наличие значит, что
        # какой-то комментарий шаблона разорван вложенным `-->` и его текст
        # утёк бы в отчёт обычным текстом.
        if "-->" in rendered:
            raise RuntimeError("в шаблоне комментарий с вложенной последовательностью '-->'")
        rendered = apply_scalars(rendered, scalars)
    except RuntimeError as error:
        print(f"Ошибка рендера шаблона: {error}", file=sys.stderr)
        return 1

    args.out_dir.mkdir(parents=True, exist_ok=True)
    out_path = args.out_dir / f"lkdr-{data['end']:%Y-%m}.html"
    out_path.write_text(rendered, encoding="utf-8")
    print(f"Отчёт обновлён: {out_path}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
