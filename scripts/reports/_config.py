#!/usr/bin/env python3
"""Настройки Python-отчётов, читаемые из config.json проекта.

Секция ai задаёт AI CLI-агента для AI-выводов отчётов:

    "ai": { "command": "codex exec --color never --sandbox read-only -" }

command — полная командная строка с аргументами (промпт подаётся агенту
на stdin), поэтому подходит любой CLI-агент: codex, claude -p и т.д.

Секция reports задаёт параметры вывода отчётов:

    "reports": { "maxItemNameChars": 40 }

Go-приложение обе секции игнорирует.
"""

from __future__ import annotations

import json
from pathlib import Path

# Промпт подаётся на stdin; '-' заставляет codex читать его оттуда.
DEFAULT_AI_COMMAND = "codex exec --color never --sandbox read-only -"

DEFAULT_MAX_ITEM_NAME_CHARS = 40

CONFIG_KEY_SECTION = "ai"
CONFIG_KEY_COMMAND = "command"
CONFIG_REPORTS_SECTION = "reports"
CONFIG_KEY_MAX_ITEM_NAME_CHARS = "maxItemNameChars"


def load_ai_command(config_path: Path = Path("config.json"), default: str | None = None) -> str:
    """ai.command из config.json; при отсутствии файла/ключа — default."""
    if default is None:
        default = DEFAULT_AI_COMMAND

    try:
        config = json.loads(config_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return default

    section = config.get(CONFIG_KEY_SECTION)
    if isinstance(section, dict):
        command = section.get(CONFIG_KEY_COMMAND)
        if isinstance(command, str) and command.strip():
            return command.strip()

    return default


def load_max_item_name_chars(
    config_path: Path = Path("config.json"),
    default: int = DEFAULT_MAX_ITEM_NAME_CHARS,
) -> int:
    """reports.maxItemNameChars из config.json; отсутствие файла/ключа — default.

    Явно заданное значение валидируется: целое число >= 1, иначе ValueError —
    опечатка в конфиге не должна молча превращаться в значение по умолчанию.
    """
    try:
        config = json.loads(config_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return default

    section = config.get(CONFIG_REPORTS_SECTION)
    if not isinstance(section, dict) or CONFIG_KEY_MAX_ITEM_NAME_CHARS not in section:
        return default

    value = section[CONFIG_KEY_MAX_ITEM_NAME_CHARS]
    if isinstance(value, bool) or not isinstance(value, int) or value < 1:
        raise ValueError(
            f"reports.{CONFIG_KEY_MAX_ITEM_NAME_CHARS}: "
            f"ожидается целое число >= 1, получено {value!r}"
        )

    return value
