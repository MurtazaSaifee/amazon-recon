# Reconciliation pipeline. Override on the command line, for example:
#   make before INPUT_DIR=/path/to/reports RECON_DATABASE_URL=postgres://...
PG_PORT      ?= 55432
RECON_DATABASE_URL ?= postgres://recon:recon@localhost:$(PG_PORT)/recon?sslmode=disable
INPUT_DIR    ?= Assignment_050726_files
PAYMENTS_FILE ?= amazon_payments_data.csv
SETTLEMENT_FILE ?= amazon_settlements_data.txt
PAYMENTS_CONFIG ?= amazon_payment_configs_au_old.csv
SETTLEMENT_CONFIG ?= amazon_settlement_configs_au.csv
SUMMARY_MAP ?= configs/summary_line_map.csv
FIXES        ?= MAPPING_FIXES.sql
OUT          ?= reports/recon_report.xlsx
BEFORE_OUT   ?= reports/before.xlsx
AFTER_OUT    ?= reports/after.xlsx
LINE         ?= Expenses/Amazon fees
export RECON_DATABASE_URL

.PHONY: db-up db-down migrate seed-config ingest apply-fixes report explain run before after test vet

db-up:
	PG_PORT=$(PG_PORT) docker compose up -d --wait

db-down:
	PG_PORT=$(PG_PORT) docker compose down -v

migrate:
	go run ./cmd/recon migrate

seed-config:
	go run ./cmd/recon seed-config --input-dir "$(INPUT_DIR)" --payments-config "$(PAYMENTS_CONFIG)" --settlement-config "$(SETTLEMENT_CONFIG)" --summary-map "$(SUMMARY_MAP)"

ingest:
	go run ./cmd/recon ingest --input-dir "$(INPUT_DIR)" --payments "$(PAYMENTS_FILE)" --settlement "$(SETTLEMENT_FILE)"

apply-fixes:
	go run ./cmd/recon apply-fixes --file "$(FIXES)"

report:
	go run ./cmd/recon report --out "$(OUT)"

# Trace one Summary line's Variance, for example: make explain LINE="Expenses/Amazon fees"
explain:
	go run ./cmd/recon explain --line "$(LINE)"

# From scratch: empty the database (--reset drops the public schema), then migrate, seed the configs, ingest and report.
run:
	go run ./cmd/recon migrate --reset
	go run ./cmd/recon seed-config --input-dir "$(INPUT_DIR)" --payments-config "$(PAYMENTS_CONFIG)" --settlement-config "$(SETTLEMENT_CONFIG)" --summary-map "$(SUMMARY_MAP)"
	go run ./cmd/recon ingest --input-dir "$(INPUT_DIR)" --payments "$(PAYMENTS_FILE)" --settlement "$(SETTLEMENT_FILE)"
	go run ./cmd/recon report --out "$(OUT)"

# The before build: the Baseline, with every interpretation switch off, from scratch.
before:
	go run ./cmd/recon migrate --reset
	go run ./cmd/recon seed-config --input-dir "$(INPUT_DIR)" --payments-config "$(PAYMENTS_CONFIG)" --settlement-config "$(SETTLEMENT_CONFIG)" --summary-map "$(SUMMARY_MAP)"
	go run ./cmd/recon ingest --baseline --input-dir "$(INPUT_DIR)" --payments "$(PAYMENTS_FILE)" --settlement "$(SETTLEMENT_FILE)"
	go run ./cmd/recon report --out "$(BEFORE_OUT)"

# The after build: the same binary, from scratch, with the mapping fixes applied and the switches on.
after:
	go run ./cmd/recon migrate --reset
	go run ./cmd/recon seed-config --input-dir "$(INPUT_DIR)" --payments-config "$(PAYMENTS_CONFIG)" --settlement-config "$(SETTLEMENT_CONFIG)" --summary-map "$(SUMMARY_MAP)"
	go run ./cmd/recon apply-fixes --file "$(FIXES)"
	go run ./cmd/recon ingest --input-dir "$(INPUT_DIR)" --payments "$(PAYMENTS_FILE)" --settlement "$(SETTLEMENT_FILE)"
	go run ./cmd/recon report --out "$(AFTER_OUT)"

test:
	go test ./...

vet:
	go vet ./...
