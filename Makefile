LKDR_DB ?= lkdr.db
LKDR_DAYS ?= 30
LKDR_TOP ?= 10
LKDR_REPORT_OUT ?= lkdr-report.txt
LKDR_CURRENCY_ARGS ?=
LKDR_COLOR ?= auto
LKDR_AI_ARGS ?=
RUN_USER ?= all

.PHONY: test bin build run clean lkdr-report lkdr-report-short lkdr-report-ai lkdr-report-file

test:
	go test -v ./...

bin/%: $(wildcard ./internal/**/*) $(wildcard ./internal/*/*) $(wildcard ./cmd/$(@:bin/%=%)/*)
	go build -o $@ -v ./cmd/$(@:bin/%=%)

bin: $(subst ./cmd,bin,$(wildcard ./cmd/*))

build: bin

run: build
	./bin/app --config.file=./config.json --log.level=DEBUG --stdin.user='$(RUN_USER)'

clean:
	rm -rf bin/*

lkdr-report:
	./scripts/lkdr_report.py --db $(LKDR_DB) --days $(LKDR_DAYS) --top $(LKDR_TOP) --color $(LKDR_COLOR) $(LKDR_CURRENCY_ARGS) $(LKDR_AI_ARGS)

lkdr-report-short:
	$(MAKE) lkdr-report LKDR_TOP=5

lkdr-report-ai:
	$(MAKE) lkdr-report LKDR_AI_ARGS="--ai-summary $(LKDR_AI_ARGS)"

lkdr-report-file:
	./scripts/lkdr_report.py --db $(LKDR_DB) --days $(LKDR_DAYS) --top $(LKDR_TOP) --color never $(LKDR_CURRENCY_ARGS) $(LKDR_AI_ARGS) > $(LKDR_REPORT_OUT)
