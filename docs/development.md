# Разработка

Команды, тесты и мок-сервис LedgerFox.

## Команды Makefile

| Команда | Назначение |
|---------|-----------|
| `make build` / `make bin` | Сборка бинарников в `bin/` (`app`, `mocklkdr`). |
| `make test` | `go test -v ./...` + автотесты Python-отчётов (`python3 -m unittest discover -s scripts/tests`). |
| `make parse` | Парсинг чеков всех пользователей из `./config.json` по очереди; приложение завершается само после последнего (`CONFIG_FILE` переопределяет путь конфига). |
| `make run` | Сборка и запуск с `./config.json`: DEBUG-логи, все пользователи из конфига (`RUN_USER=all` по умолчанию). |
| `make clean` | Очистка `bin/`. |
| `make report` | Интерактивное меню Python-отчётов из `scripts/reports/`; аргументы пробрасываются выбранным скриптам: `make report REPORT_ARGS="--db my.db"`. |
| `make ai-report` | HTML-отчёт «Месяц в чеках» в `reports/lkdr-YYYY-MM.html` (месяц — из конца периода: свежий чек или `--as-of`; перезапуск обновляет файл того же месяца). AI-выводы через Codex CLI, при недоступности — детерминированные из данных. Аргументы: `AI_REPORT_ARGS`, база — `LKDR_DB`. |
| `make lkdr-report` и др. | Отчёты по покупкам — [Отчёт по покупкам LKDR](lkdr-report.md). |
| `scripts/dist.sh` | Релизные архивы в `bin/` для windows/linux/darwin × amd64/arm64 (матрица сужается `GOOSES=… GOARCHES=…`; кросс требует C-компилятор из-за SQLite/CGO). |

Отдельный тест:

```bash
go test -v ./path/to/pkg -run TestName
```

## Интеграционные тесты и мок-сервис

Интеграционные тесты (`internal/jobs/lkdr/integration_test.go`) прогоняют весь
конвейер задачи `lkdr` — реальный клиент `lkdr-api`, загрузчики и SQLite — против
мок-сервиса API ФНС. Ответы детерминированы и не зависят от сети.

Как устроена подмена API:

- в `lkdr-api` базовый URL зашит (`https://mco.nalog.ru/api`), но транспорт
  `http.RoundTripper` инъектируем;
- `NewRedirectTransport` (`internal/jobs/lkdr/lkdr.go`) переадресует запросы на
  адрес мока, сохраняя путь, тело и заголовки;
- в тестах мок поднимается через `httptest.Server`, в рабочем приложении — через
  параметр `lkdr.apiUrl`.

Мок (`internal/mocklkdr`) отдаёт фиксированный набор: 3 чека за сентябрь 2026,
2 бренда, фискальные детали с товарами; любая капча и любой SMS-код проходят.
Отдельный запуск — в docker:

```bash
docker compose up -d mocklkdr   # мок на http://127.0.0.1:18080
```

Подключение приложения: `"lkdr": { "apiUrl": "http://127.0.0.1:18080", … }`.
Без docker: `go run ./cmd/mocklkdr -addr :8080`.

## Python-отчёты: меню и свои скрипты

`make report` показывает интерактивное меню всех Python-отчётов и запускает
выбранный (номер или id, `q` — выход). Отчёты лежат в `scripts/reports/`
и находятся автоматически:

- каждый `*.py` в `scripts/reports/` — отдельный отчёт, id = имя файла
  без расширения;
- файлы с префиксом `_` служебные и в меню не попадают (`_template.py`);
- заголовок пункта меню — первая строка docstring скрипта.

Чтобы добавить свой отчёт:

1. Скопируйте шаблон: `cp scripts/reports/_template.py scripts/reports/my_report.py`.
2. Первой строкой docstring задайте заголовок для меню.
3. Пишите на стандартной библиотеке Python 3.10+; базу открывайте только
   для чтения, личные данные не логируйте.
4. `make report` — новый скрипт уже в меню. Без меню: `./scripts/report.py my_report [аргументы]`
   (для cron и скриптов) и `./scripts/report.py --list` — список отчётов.
5. Добавьте автотесты в `scripts/tests/test_reports.py` — они запускаются
   вместе с Go-тестами командой `make test`.

Готовый пример по всем правилам — `scripts/reports/example.py`.

## Python-окружение для отчётов

`scripts/reports/lkdr_report.py` использует только стандартную библиотеку; нужен Python 3.10+:

```bash
python3 -m venv .venv
.venv/bin/python scripts/reports/lkdr_report.py --help
PATH="$PWD/.venv/bin:$PATH" make lkdr-report
```

`.venv` не коммитится (Python 3.14 создаёт внутренний `.venv/.gitignore`).
