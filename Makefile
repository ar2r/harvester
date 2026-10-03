LKDR_DB ?= lkdr.db
LKDR_DAYS ?= 30
LKDR_TOP ?= 10
LKDR_REPORT_OUT ?= lkdr-report.txt
LKDR_CURRENCY_ARGS ?=
LKDR_COLOR ?= auto
LKDR_FORMAT ?= text
LKDR_AI_ARGS ?=
RUN_USER ?= all
CONFIG_FILE ?= ./config.json
REPORT_ARGS ?=
AI_REPORT_ARGS ?=

.PHONY: test bin build run parse report report-all ai-report clean lkdr-report lkdr-report-short lkdr-report-ai lkdr-report-file

test:
	go test -v ./...
	python3 -m unittest discover -s scripts/tests -v

bin/%: $(wildcard ./internal/**/*) $(wildcard ./internal/*/*) $(wildcard ./cmd/$(@:bin/%=%)/*)
	go build -o $@ -v ./cmd/$(@:bin/%=%)

bin: $(subst ./cmd,bin,$(wildcard ./cmd/*))

build: bin

run: build
	./bin/app --config.file='$(CONFIG_FILE)' --log.level=DEBUG --stdin.user='$(RUN_USER)'

parse: build
	./bin/app --config.file='$(CONFIG_FILE)' --stdin.user=all

report:
	./scripts/report.py $(REPORT_ARGS)

ai-report:
	./scripts/reports/ai_report.py --db $(LKDR_DB) $(AI_REPORT_ARGS)

# Всё сразу и без вопросов: обновить HTML-отчёт месяца, затем напечатать
# текстовый отчёт с AI-выводами и рекомендациями в консоль.
report-all: ai-report lkdr-report-ai

clean:
	rm -rf bin/*

lkdr-report:
	./scripts/reports/lkdr_report.py --db $(LKDR_DB) --days $(LKDR_DAYS) --top $(LKDR_TOP) --color $(LKDR_COLOR) --format $(LKDR_FORMAT) $(LKDR_CURRENCY_ARGS) $(LKDR_AI_ARGS)

lkdr-report-short:
	$(MAKE) lkdr-report LKDR_TOP=5

lkdr-report-ai:
	$(MAKE) lkdr-report LKDR_AI_ARGS="--ai-summary $(LKDR_AI_ARGS)"

lkdr-report-file:
	./scripts/reports/lkdr_report.py --db $(LKDR_DB) --days $(LKDR_DAYS) --top $(LKDR_TOP) --color never $(LKDR_CURRENCY_ARGS) $(LKDR_AI_ARGS) > $(LKDR_REPORT_OUT)
