# Reconciliation pipeline. Override on the command line, for example:
#   make run INPUT_DIR=/path/to/reports RECON_DATABASE_URL=postgres://...
PG_PORT      ?= 55432
RECON_DATABASE_URL ?= postgres://recon:recon@localhost:$(PG_PORT)/recon?sslmode=disable
INPUT_DIR    ?= Assignment_050726_files
PAYMENTS_FILE ?= amazon_payments_data.csv
SETTLEMENT_FILE ?= amazon_settlements_data.txt
OUT          ?= reports/recon_report.xlsx
export RECON_DATABASE_URL

.PHONY: db-up db-down migrate ingest report run test vet

db-up:
	PG_PORT=$(PG_PORT) docker compose up -d --wait

db-down:
	PG_PORT=$(PG_PORT) docker compose down -v

migrate:
	go run ./cmd/recon migrate

ingest:
	go run ./cmd/recon ingest --input-dir "$(INPUT_DIR)" --payments "$(PAYMENTS_FILE)" --settlement "$(SETTLEMENT_FILE)"

report:
	go run ./cmd/recon report --out "$(OUT)"

# From scratch: empty the database (--reset drops the public schema), then migrate, ingest and report.
run:
	go run ./cmd/recon migrate --reset
	go run ./cmd/recon ingest --input-dir "$(INPUT_DIR)" --payments "$(PAYMENTS_FILE)" --settlement "$(SETTLEMENT_FILE)"
	go run ./cmd/recon report --out "$(OUT)"

test:
	go test ./...

vet:
	go vet ./...
