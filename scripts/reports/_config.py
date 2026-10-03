#!/usr/bin/env python3
"""Настройки Python-отчётов, читаемые из config.json проекта.

Секция ai задаёт AI CLI-агента для AI-выводов отчётов; Go-приложение
эту секцию игнорирует. Формат:

    "ai": { "command": "codex exec --color never --sandbox read-only -" }

command — полная командная строка с аргументами (промпт подаётся агенту
на stdin), поэтому подходит любой CLI-агент: codex, claude -p и т.д.
"""

from __future__ import annotations

import json
from pathlib import Path

# Промпт подаётся на stdin; '-' заставляет codex читать его оттуда.
DEFAULT_AI_COMMAND = "codex exec --color never --sandbox read-only -"

CONFIG_KEY_SECTION = "ai"
CONFIG_KEY_COMMAND = "command"


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
