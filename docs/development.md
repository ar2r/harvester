# Разработка

Команды, тесты и мок-сервис LedgerFox.

## Команды Makefile

| Команда | Назначение |
|---------|-----------|
| `make build` / `make bin` | Сборка бинарников в `bin/` (`app`, `mocklkdr`). |
| `make test` | `go test -v ./...`. |
| `make run` | Сборка и запуск с `./config.json`: DEBUG-логи, все пользователи из конфига (`RUN_USER=all` по умолчанию, `RUN_JOBS=lkdr`). |
| `make clean` | Очистка `bin/`. |
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

## Python-окружение для отчётов

`scripts/lkdr_report.py` использует только стандартную библиотеку; нужен Python 3.10+:

```bash
python3 -m venv .venv
.venv/bin/python scripts/lkdr_report.py --help
PATH="$PWD/.venv/bin:$PATH" make lkdr-report
```

`.venv` не коммитится (Python 3.14 создаёт внутренний `.venv/.gitignore`).
