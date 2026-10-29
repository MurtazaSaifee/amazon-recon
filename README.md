# Amazon Payments vs Settlement reconciliation

Reads an Amazon Payments report and a Settlement report, matches every line to a config-driven rule, routes its amounts to Summary fields, and reconciles the two by `record_ref`. The result is an Excel workbook whose Summary balances to the Settlement total and in which every number traces to a source line.

No LLM is in the money path. All money is `NUMERIC(14,2)` in PostgreSQL and `shopspring/decimal` in Go.

## What I found (step 5)

The Baseline, with every interpretation switch off, had eight non-zero lines: seven Summary lines and one field with no Summary line. They have five root causes, one row per cause.

| Summary line | Baseline Variance | Root cause | Fix (kind) | After |
|---|---:|---|---|---|
| Expenses › Amazon fees | -133,756.51 | The Transfer's description carries account digits, so P-063 misses it and Catch-all P-110 sends it to Amazon fees | Interpretation switch `prefix_before_catch_all`; P-063 unchanged | 0.00 (the Transfer is shown on the Tie-out) |
| Expenses › Promo rebates; Expenses › Other | +11,273.53; -11,273.53 | P-065 routes ORDER promotional rebates to Other; the settlement routes the same promotions to Promo rebates | Mapping defect: P-065 to Promo rebates | 0.00; 0.00 |
| Sales › Shipping; Sales › Product Charges; Sales › Other | +13,572.17; -92.84; -11.97 | Duplicate rules P-005 and P-072 double-count 13,478.97, and one payments tax column cannot split across four settlement components | Mapping defect: delete P-005 and P-072. Schema limitation plus policy: all sales tax to Sales › Tax | 0.00; 0.00; 0.00 |
| Refunds › Refund expenses | +42.59 | P-014 (refund tax) has no Summary field, and the settlement sends the same tax to Refund expenses | Accounting-policy call: refund tax to Sales › Tax (P-014, S-004, S-011, S-020) | 0.00 |
| No Summary line (`sales_gift_wrap_credits`) | +11.61 | P-070 routes gift wrap to a field with no Summary line | Mapping defect: P-070 to Sales › Other | 0.00 (Sales › Other 11.61 on both sides) |

The Summary then tied, and "the two columns agree" was treated as necessary, not sufficient: pass B audited the meaning of every rule that fires. It applied P1 (the FBA fulfilment fee moved from Amazon fees to FBA fees on both sides, -96,803.17), an accounting-policy call the author decided, and six objective latent defects (O1–O6) that do not fire in this settlement.

The 26 unreconciled payments cannot be closed: the Transfer (-133,756.51) is a bank payout, not P&L, so it is shown on the Tie-out, and the 25 zero-value Order lines have no settlement row to pair with.

## Quick start

**Prerequisites:** Go 1.27, and either Docker or any PostgreSQL 17.

```sh
make db-up                       # PostgreSQL 17 in Docker on localhost:$PG_PORT (default 55432)
```

`make db-up` runs `PG_PORT=… docker compose up -d --wait`. The Makefile sets `RECON_DATABASE_URL` to the compose database if you have not set it. **The tool reads only `RECON_DATABASE_URL`, never `DATABASE_URL`**, so a personal `DATABASE_URL` cannot point a reset at the wrong database. `migrate --reset` also refuses any database whose name does not start with `recon`, unless `--force-reset` is given.

Put the input files in `Assignment_050726_files/` (the default), or pass `INPUT_DIR=/path/to/reports`:

- `amazon_payments_data.csv`
- `amazon_settlements_data.txt`
- `amazon_payment_configs_au_old.csv`
- `amazon_settlement_configs_au.csv`

The Summary-line map is committed in `configs/summary_line_map.csv`. The input data is not in this repository (see the last section).

```sh
make before     # Baseline: migrate --reset, seed configs, ingest with every switch off, report -> reports/before.xlsx
make after      # Fixed: migrate --reset, seed configs, apply MAPPING_FIXES.sql, ingest, report -> reports/after.xlsx
make test       # Unit tests, and pipeline tests when RECON_DATABASE_URL is set
```

The CLI, one step at a time (`go run ./cmd/recon <command>`):

```sh
recon migrate     [--db URL] [--reset [--force-reset]]
recon seed-config [--input-dir DIR] [--payments-config FILE] [--settlement-config FILE] [--summary-map FILE]
recon apply-fixes [--file MAPPING_FIXES.sql]
recon ingest      [--input-dir DIR] [--payments FILE] [--settlement FILE] [--baseline]
recon report      [--out reports/recon_report.xlsx]
recon explain     --line "Expenses/Amazon fees"
recon counts
```

`--db` defaults to `$RECON_DATABASE_URL`. `make explain LINE="Expenses/Amazon fees"` runs `explain`.

## Flow

**ingest → reconcile → report**

| Step | Package | What it does |
|---|---|---|
| Entry point | `cmd/recon` | CLI commands and flags |
| Read | `internal/source` | Streams both reports, decides scope, returns malformed lines as values |
| Read config | `internal/configcsv` | Reads the mapping configs and Summary-line map as given |
| Map | `internal/mapping` | Pure: one line to a `record_ref`, Contributions and warnings. No DB, no file IO |
| Orchestrate | `internal/ingest` | Readers, then scope, then mapping, then store, in one transaction per run |
| Store | `internal/store` | Migrations, COPY bulk writes, config tables, the variance views, `apply-fixes` |
| Reconcile | `internal/recon` | Set-based SQL, returns plain report data |
| Report | `internal/report` | Writes the Excel workbook. No DB access |
| Explain | `internal/explain` | Traces one Summary line's Variance to its rules and record_refs |

## Schema and why

Migrations are in `internal/store/migrations` and applied in filename order.

- **`ingest_runs`**, **`ingest_files`**, **`ingest_errors`**: one run per ingest, with each file's SHA-256 and line counts. A run is complete only after its rows are stored, and reports read the latest complete run. Malformed lines go to `ingest_errors`. A malformed line that is in scope, or whose scope cannot be read, fails the run before it replaces anything; one out of scope is recorded and ingestion goes on. Re-ingesting the same files is therefore safe.
- **`source_rows`**: both reports at line grain, with the file name, line number and raw cells (`raw_payload` as JSONB). *Why one table at line grain:* every number in the report traces to a file and line. Both sources share one scope and reconciliation path. Amounts are `NUMERIC(14,2)`.
- **`payment_mapping_rules`**, **`settlement_mapping_rules`**, **`summary_line_map`**: the configs as data, verbatim. A rule ID is its CSV line number (`P-071`, `S-065`), so a rule can be looked up in the file it came from.
- **`mapping_rule_history`**: a trigger writes every INSERT, UPDATE and DELETE on the mapping tables, with the old and new row. Fixes are auditable.
- **`summary_contributions`**: one row per line, per matched rule and per amount routed to a Summary field. *Why:* the Summary is the sum of its Contributions, so any Summary number can be audited down to a source line.
- **`summary_running`**: running Summary totals per run, source and field, updated per batch inside the ingest transaction. *Why:* the report cross-checks them against a SQL recompute from the Contributions, so a mismatch is caught rather than printed.
- **Views**: `reconciliation_by_record_ref` (one row per `record_ref`, with its status), `v_line_variance` and `v_record_variance` (plain SQL over the Contributions, which `recon explain` reads).
- **`ingest_runs`** also records each run's `mode`, `switches` and `config_hash` (migrations 0004 and 0006). A report states which switches produced its numbers.

**Bulk writes use `COPY`**, in batches of 5,000 lines. The foreign key from `summary_contributions` to `source_rows` is checked as each batch lands (see Performance).

## Assumptions

**Scope.** In scope means the Released payments lines and the settlement components of the settlement named in the Settlement report's header. Deferred lines and other settlements are still ingested, but they sit outside scope on the **Out of Scope** sheet with a reason. The Deferred total is shown as a memo.

**Posted date.** The `date` in `record_ref` is a UTC posted date. For a Released payments line it is `Transaction Release Date` (GMT+9, converted to UTC). For a Deferred line it is `date/time` (also converted). For a settlement component it is the report's `posted-date`. The payments `date/time` is the order date, so it does not match the settlement; the release date does, to the second.

**`record_ref`.** Each mapping rule has a `+`-separated template. A token that names a key (`txn_ref`, `sku`, `date`, `settlement_id`, `description`, `shipment_id`, `merchant_order_id`) resolves to the line's own value. Any other token is literal text. A field-name-shaped token that is not a key raises a warning. Parts are joined with `|`, and an empty part stays empty.

**Normalisation.** The same rule is applied to config keys and source values: trim, upper-case, and each run of whitespace becomes `_`. Nothing else: no dash folding, no punctuation stripping, no fuzzy matching.

**Matching precedence.** The highest tier with any matching rule wins, and only that tier applies:

1. Transaction type and exact description
2. Transaction type and wildcard description
3. Transaction type and a description that is a prefix of the line's, ending at `_` or `:` (switch `prefix_before_catch_all`)
4. Catch-all (blank type) and exact description
5. Catch-all and wildcard

Settlement rules also match the amount type. A line that matches nothing is Unmapped.

**Multi-rule hits.** Every rule in the winning tier applies, and a warning names the Amount field and the rules. A duplicated rule therefore counts twice. The warning is stored with the line, so the double count is visible rather than silent.

**Sign routing.** A positive amount goes to the rule's positive field, and a negative amount to its negative field. A blank field routes nothing. A zero amount produces no Contribution.

**Granularity.** A payments line has one order item and about ten typed amount columns. A settlement has one component per amount type. Each side is summed by `record_ref`, and the payments line total is compared with the sum of the components.

## Interpretation switches

Switches are fixes to the matching logic, not to the config. They are recorded per run.

- **`prefix_before_catch_all`** (on in a fixed run, off with `--baseline`). A rule of the same transaction type can match when its description is a prefix of the line's, ending at `_` or `:`, and this is tried before the Catch-all. *Why it is matching logic:* the transfer rule's description is correct as a prefix of the bank transfer text. An exact match reads the real text as different, because the text carries the account's last digits, so the line falls to the Catch-all and is summed as a fee. Adding the digits to the config would store account data in the config, and it would not fix the next account. The before build keeps the switch off, so the defect shows in the Baseline.

## Results

Figures from the after build (`make after`, reconciling the Settlement with the fixes applied). Variance is Payments minus Settlements.

| Summary line | Payments | Settlements | Variance |
|---|---:|---:|---:|
| Sales | 357,758.80 | 357,758.80 | 0.00 |
| Sales › Product Charges | 335,336.96 | 335,336.96 | 0.00 |
| Sales › Tax | 13,436.38 | 13,436.38 | 0.00 |
| Sales › Shipping | 8,294.16 | 8,294.16 | 0.00 |
| Sales › Amazon fees | 0.00 | 0.00 | 0.00 |
| Sales › Inventory Reimbursements | 679.69 | 679.69 | 0.00 |
| Sales › Cross-account Debt Adjustment | 0.00 | 0.00 | 0.00 |
| Sales › Other | 11.61 | 11.61 | 0.00 |
| Sales › FBA Fees | 0.00 | 0.00 | 0.00 |
| Sales › Micro Deposit (Failed) | 0.00 | 0.00 | 0.00 |
| Refunds | -1,772.50 | -1,772.50 | 0.00 |
| Refunds › Refund expenses | 258.87 | 258.87 | 0.00 |
| Refunds › Refunded sales | -2,031.37 | -2,031.37 | 0.00 |
| Expenses | -143,867.35 | -143,867.35 | 0.00 |
| Expenses › Promo rebates | -11,273.53 | -11,273.53 | 0.00 |
| Expenses › FBA fees | -96,803.58 | -96,803.58 | 0.00 |
| Expenses › Cost of Advertising | 0.00 | 0.00 | 0.00 |
| Expenses › Shipping Charges | 0.00 | 0.00 | 0.00 |
| Expenses › Amazon fees | -35,790.24 | -35,790.24 | 0.00 |
| Expenses › Reversed Reimbursements | 0.00 | 0.00 | 0.00 |
| Expenses › Cross-account Debt Adjustment | 0.00 | 0.00 | 0.00 |
| Expenses › Other | 0.00 | 0.00 | 0.00 |
| Expenses › Micro Deposit | 0.00 | 0.00 | 0.00 |
| Paid To Amazon | 0.00 | 0.00 | 0.00 |
| Summary fields with no Summary line | none | none | 0.00 |

**Tie-outs.**

- Settlement total (header) 212,118.95. In-scope components sum to 212,118.95 over 54,979 lines, difference 0.00.
- Payments: Released lines excluding the Transfer sum to 212,118.95 over 13,471 lines, and the Summary net is 212,118.95. The one Released line outside the Summary is the Transfer (−133,756.51). Unexplained difference 0.00.
- Deferred memo (out of scope): 43,540.07 over 2,398 lines.

**Counts** (record_refs; Unmapped counts lines):

| Status | Count |
|---|---:|
| Reconciled | 13,289 |
| Reconciled with difference | 0 |
| Unreconciled payment | 26 |
| Unreconciled settlement | 0 |
| Unmapped | 0 |

**The 26 unreconciled payments**, verified by SQL on the after run:

- **The Transfer (1).** A bank payout of −133,756.51, not a sale. It matches the transfer rule, which has no Summary field, so it sits outside the Summary on purpose and is shown on the Tie-out.
- **Zero-value Order lines (25).** Every amount column on these lines is 0.00, and the settlement report has no Component for any of their orders. Zero-amount Components do occur in the file, but only beside non-zero Components of the same record, so there is nothing to pair these 25 with.

## Self-checks: how the pipeline catches its own mistakes

- **Fix files apply all or nothing.** `apply-fixes` runs the whole file in one transaction, and commits nothing if a guarded statement changes no rows (`internal/store`; `TestApplyFixesIsOneTransaction`).
- **A stale config is refused.** A report refuses a run whose mapping configs have changed since ingest, and asks for ingest to be re-run (`internal/recon`; `TestReportRefusesRunWhoseConfigChanged`).
- **Running totals are recomputed.** The Summary's running totals are checked against a SQL recompute from the Contributions. A mismatch fails the report and names the field (`internal/recon`; `TestRecomputeMismatchFailsReport`).
- **The tie-out has no plug.** "Unexplained difference (must be 0.00)" is the residual minus the Unreconciled amounts read from reconciliation status, with no hardcoded adjustment (`internal/recon`, `internal/report`).
- **Ambiguity is warned and stored.** A winning tier with several rules for one Amount field, and a record_ref token that looks like a field name but is not one, each raise a warning stored with the line (`internal/mapping`; `source_rows.warnings`).
- **A malformed in-scope line stops the run.** The run fails before anything is replaced, and the previous complete run stays the latest (`internal/ingest`; `TestMalformedLineInScopeFailsTheRun`).
- **Re-ingesting replaces, it does not add.** Ingesting the same files again replaces the stored lines, Contributions and totals, and leaves the counts unchanged (`internal/store`; `TestReingestReplacesEarlierRowsAndKeepsCountsAndTotals`).
- **Resets are guarded.** `migrate --reset` refuses a database whose name does not start with `recon`, unless `--force-reset` is given. The tool reads only `RECON_DATABASE_URL` (`cmd/recon`; `TestCheckResetTarget`).

## Where to look

- **`PROGRESS.md`**: the investigation log, from exploration to the final counts, with each variance traced to its rules.
- **`MAPPING_FIXES.sql`**: each fix as a commented block (defect, rule, old and new value, evidence, Variance closed), plus the interpretation switch and the schema limitation, and the accounting-policy calls.
- **The workbook**, after `make before` and `make after`: **Summary** (with the Tie-out), **Consolidated Data** (one row per record_ref, then one per unmapped line, with source line numbers), **Audit Trail** (every Contribution), **Out of Scope**.
- **`recon explain --line "<group>/<line>"`**: for a Summary line, the Variance, the Contributions by rule on each side, and the largest record_refs with their source lines.

## Performance

A full build (reset, seed, ingest, report) takes about 12–13 s on about 78,000 stored lines. Ingest was waiting on the database, not computing: after a reset, the planner had no statistics for the new `source_rows` table, so each batch's foreign-key check scanned it. Running `ANALYZE` on `source_rows` once its rows are loaded, and on `summary_contributions` at the end of the ingest, fixes the plan.

## Tests

- **Unit tests** (`internal/mapping`, `cmd/recon`) need no database: normalisation, record_ref templates, amount fields, the matching tiers and the switch registry, and the reset guard.
- **Pipeline tests** (`internal/pipelinetest`) run synthetic report files through ingest, reconciliation, the Summary, the variance views, `explain`, mapping fixes, malformed lines, safe re-runs and the workbook. They need `RECON_DATABASE_URL` and skip with a message when it is unset. Each test creates its own database.

Run them with `make test`, or `RECON_DATABASE_URL=… go test ./...`.

## Not in this repository, and why

The two reports contain seller data (buyer locations, order IDs), and so do the generated workbooks and the PostgreSQL dump. The mapping configs and the sample workbook are PortOne's files, and they are not redistributed. All of them go with the submission email instead, and `.gitignore` excludes them.

Identifiers from the real files are redacted in this repository's history: the settlement ID appears as 90000000001, the other settlement IDs in the payments file as 90000000002 to 90000000004, and the bank account's last digits as 999. The workbooks and dump sent with the email carry the real values.

## How it was built

- Built with Claude Code, agentic and test-first. An Opus session acted as orchestrator and reviewer: exploration, design, tickets, review, and the read-only SQL tracing of every Variance.
- Haiku subagents implemented every ticket test-first, each in its own git worktree, and a merger agent integrated them.
- A two-axis code review (repository standards; does the code do what the ticket asked) was followed by one fix pass.
- The author made every accounting-policy decision. Each was presented with its evidence and its exact effect per Summary line before anything was applied.
- No LLM is in the money path: the pipeline is deterministic Go and SQL, and every reported number was verified by SQL against source rows.
