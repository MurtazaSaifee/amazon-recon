# Progress log

A running record of what I tried, what I ruled out, and how I isolated each variance.

## 2026-10-28: Data exploration (before writing any code)

### The anchor number
- The settlement file covers one settlement, `90000000001` (17.07.2026 to 31.07.2026 UTC). Its header row states `total-amount = 212,118.95 AUD`.
- The settlement component rows sum to **212,118.95**, so the file agrees with its own header.
- The payments report covers **4** settlement IDs (`90000000002`, `90000000003`, `90000000001`, `90000000004`).
- Payments rows with `settlement ID = 90000000001`, `Transaction status = Released`, excluding `Transfer`, also sum to **212,118.95**. This is the scope that must tie.
  - The **Deferred** rows in that settlement (2,398 rows, 43,540.07) have no release date. They belong to a later disbursement, so they are expected to be unreconciled.
  - The **Transfer** row (-133,756.51 on 18 Jul) is the payout of the *previous* settlement to the bank account. It is not a component of this settlement.

### Normalisation the ingestion has to do (files are ingested as-is)
- Payments CSV: UTF-8 BOM, 9 preamble lines before the header, and thousands separators in amounts (`"-133,756.51"`).
- Payments dates are `GMT+9` and settlement dates are `UTC`. In payments, `date/time` is the *order* date, but `Transaction Release Date` converted to UTC equals the settlement's `posted-date-time` to the second. Example: order `200-0000000-0000001` has payments date/time 9 Jul, release date 17 Jul 4:26:32 pm GMT+9, and settlement posted-date-time 17.07.2026 07:26:32 UTC. So **`date` in `record_ref` must be the UTC posted date**.
- Type and description values need upper-casing, with spaces turned into `_`, to match config keys. For example, `other-transaction` becomes `OTHER-TRANSACTION`, `Service fee` becomes `SERVICE_FEE`, and `FBA Inventory Reimbursement - Damaged:Warehouse` becomes `FBA_INVENTORY_REIMBURSEMENT_-_DAMAGED:WAREHOUSE`.
- Payment column headers map to the config's `amount_field`. For example, `product sales` becomes `product_sales`, and `fulfilment by amazon fees` becomes `fba_fees`, which needs an alias.

### Granularity
- A payments row is one transaction line with about 10 typed amount columns. Each config rule (type, description, amount_field) turns one column into one summary contribution.
- A settlement row is one amount component (amount_type / amount_description).
- I plan to aggregate each side by `record_ref` and then compare: the payments `total` against the sum of the settlement components.

### Hypotheses about config defects (not yet verified, only from reading the configs)
1. Payments `ORDER / low_value_goods` appears twice (→ `sales_shipping` **and** `sales_product_charges`). This would double-count.
2. Payments `ORDER / sales_tax_collected` appears twice the same way. Settlement splits `Tax` (→ product charges) from `ShippingTax` (→ shipping), but payments has a single tax column. This may not be expressible in the config schema.
3. Payments `ORDER / promotional_rebates` maps to `expenses_other`, while settlement `ORDER / Promotion / *` maps to `expenses_promotional_rebates`.
4. Settlement `ORDER / Promotion / TaxDiscount` maps to `sales_shipping`.
5. Payments `ORDER / gift_wrap_credits` maps to `sales_gift_wrap_credits`, which has no line on the Summary sheet. Settlement `GiftWrap` maps to `sales_other`.
6. Payments `REFUND / sales_tax_collected` has no summary field. Settlement `REFUND / ItemPrice / Tax` routes by sign to `total_refund_expense_or_sales_amt` or `refunded_expenses`.
7. The payments catch-all row (`transaction_type` empty, `any`, `total` → `expenses_amazon_fees`) would absorb the Transfer row unless `TRANSFER / TO_ACCOUNT_ENDING` matches `To account ending with: 999`. This may be a matching-logic question (prefix match) rather than a config defect.

Each of these gets confirmed or ruled out against the actual Summary deltas once the pipeline runs.

### Facts checked while designing
- With minimal normalisation, every in-scope payments line and settlement line matches at least one mapping rule. The one exception is the **Transfer** line (`TO_ACCOUNT_ENDING_WITH:_999`). No `TRANSFER` rule matches it exactly, so it falls through to the catch-all rule (empty `transaction_type`), which routes `total` to `expenses_amazon_fees`. Expected in the baseline: a −133,756.51 difference on Expenses › Amazon fees.
- In the payments config, all rules for a given (transaction_type, description) share one `record_ref` template. So `record_ref` belongs to the line as a whole, not to each amount column.
- The payments config has exactly two duplicated rules, where two rules route the same amount column: `ORDER/any/low_value_goods` and `ORDER/any/sales_tax_collected`. The settlement config has none.

## 2026-10-28: Design decisions (grilling session)

Three rounds of design questions, all settled before any code was written. The reasoning behind each is written up in the README's "Schema" and "Assumptions" sections.

- **Scope.** In scope means the Released lines of the settlement named in the settlement report's header. Deferred lines and other settlements are still ingested and are reported separately with a reason.
- **Key date.** The `date` in `record_ref` is the UTC posted date, taken from `Transaction Release Date`. The payments `date/time` is the order date in GMT+9 and would not match.
- **Literal baseline.** The baseline uses minimal normalisation and exact description matching, and applies every rule that hits the same amount, even when that double-counts. Rule precedence is decided once per line. Fixes to the matching logic sit behind named switches, and `--baseline` turns all of them off, so one binary produces both the before and after reports.
- **Schema.** `source_rows` is a single table for both files at line grain, holding the raw payload, source file and line number. `summary_contributions` stores one row per line and matched rule.
- **Reconciliation.** It compares each line's own amount (payments `total` against the settlement `amount`), not the mapped amounts. Mapping defects and key-template defects therefore show up in different places.
- **Summary during ingestion.** Totals are accumulated per batch inside the ingest transaction, and the report checks them against a SQL recomputation.

Other settled points:
- **Report.** The Summary keeps source signs. Subtotals and differences are formulas with stored values. Summary fields with no Summary line get their own block. A Tie-out block per source goes back to the 212,118.95 Settlement total. The Consolidated sheet has one row per record_ref, with source line numbers. An Audit Trail sheet lists every Contribution.
- **Step 5 method.** Pass A is driven by Variances (`recon explain --line …` plus variance views). Pass B checks the meaning of every rule that fires, which catches rules that are wrong on both sides in the same way. Objective defects are fixed. Accounting-policy calls go in a separate commented block of `MAPPING_FIXES.sql`.
- **Robustness.** `ingest_runs` makes re-runs safe (a file's rows are replaced in one transaction). Malformed lines go to `ingest_errors`. A change log on the config tables records every config change.
- **Data handling.** The input files carry buyer city, state and postcode alongside order IDs. The repo is public, so it contains no input data, full dump or generated reports. Those go with the submission email. Tests use small synthetic fixtures built to the same file shapes.
- **AI.** There is no LLM in the money path. Claude Code is used for exploration and for adversarial checking of hypotheses. An optional `suggest` command comes only if time allows.

## 2026-10-28: Ticket 01, tracer bullet (raw lines tie to the Settlement total)

- The first ingest run on the real payments file failed. Its GMT+9 timestamps use both full and abbreviated months ("3 July 2026" and "1 Aug 2026"), and the reader had only handled full names. It refused 1,153 lines and replaced nothing, which is the intended failure. Both spellings are now accepted and the file has no malformed lines.
- `make run` on the real input (ticket database, from scratch):
  - Settlement total (header) 212,118.95.
  - In-scope Components 212,118.95 over 54,979 lines, difference 0.00.
  - In-scope Released excluding Transfer 212,118.95 over 13,471 lines, difference 0.00.
  - Transfer (in scope) -133,756.51, one line. Released including Transfer 78,362.44 over 13,472 lines.
  - Deferred memo (out of scope) 43,540.07 over 2,398 lines.
- Scope: 13,472 Released, 2,398 Deferred and 7,156 other-Settlement payments lines, which total 23,026. Settlement: 54,979 components in scope plus the header.
- Re-ingesting the same files keeps the stored rows at 78,006 (23,026 payments plus 54,980 settlement), so nothing is double-counted.
- Synthetic fixtures copy the real shapes: BOM, 9-line preamble, quoted thousands, release times either side of midnight UTC and exactly at midnight, Deferred lines, another Settlement, the header line and a Transfer line.

## 2026-10-28: Ticket 03, config-driven Summary during ingestion (Baseline, no switches)

Run 1 on the real files, seeded from `amazon_payment_configs_au_old.csv` (149 rules, P-002 to P-150), `amazon_settlement_configs_au.csv` (137 rules) and `configs/summary_line_map.csv` (21 Summary lines). No interpretation switches exist yet, so this is the Baseline. Variance is Payments minus Settlements.

- Expenses › Amazon fees: variance -133,756.51 (Payments -266,349.92, Settlements -132,593.41). This is the Transfer line, which is in scope and falls to the catch-all rule.
- Other non-zero Summary lines (Payments, Settlements, variance):
  - Sales › Product Charges: 348,815.93, 348,908.77, -92.84
  - Sales › Shipping: 21,773.13, 8,200.96, 13,572.17
  - Sales › Other: 0.00, 11.97, -11.97
  - Refunds › Refund expenses: 258.87, 216.28, 42.59
  - Expenses › Promo rebates: 0.00, -11,273.53, 11,273.53
  - Expenses › Other: -11,273.53, 0.00, -11,273.53
- Unmapped Summary field `sales_gift_wrap_credits`: payments 11.61, settlements 0.00.
- Tie-out, Payments: Summary net 91,884.00; routing gaps -13,521.56 (group P-004,P-009 to P-019: -42.59; group P-005,P-006,P-064 to P-074: -13,478.97); in-scope total 78,362.44; remaining difference to the Settlement total 212,118.95 is -133,756.51, the Transfer.
- Tie-out, Settlements: Summary net 212,118.95; routing gaps 0.00; in-scope total 212,118.95; remaining difference 0.00.
- Deferred memo (out of scope): 43,540.07 over 2,398 lines.
- Subtotals: Sales payments 371,268.75 / settlements 357,801.39; Refunds -1,772.50 / -1,815.09; Expenses -277,623.86 / -143,867.35; Paid To Amazon 0.00 / 0.00.
- Running totals equal the SQL recomputation from the Contributions (the report did not fail).

## 2026-10-28: Ticket 04, reconciliation by record_ref (Baseline, run 1 on the real files)

Each source is grouped by record_ref over its in-scope mapped lines. The payments line `total` is compared with the sum of Component amounts. Counts are record_refs, except Unmapped, which counts lines.

- Reconciled: 13,289
- Reconciled with difference: 0
- Unreconciled payment: 26
- Unreconciled settlement: 0
- Unmapped: 0 lines

Checks:
- An independent SQL query (sum of payments per record_ref against sum of Components per record_ref, outside the view) finds the same 13,289 records with both sides, and none of them differ.
- The Transfer line is an Unreconciled payment, record_ref `|90000000001|2026-07-18`, -133,756.51.
- Of the 26 Unreconciled payments, 25 are Order lines whose `total` is 0.00 and which have no Component. The Transfer is the only one with an amount. Amazon's Settlement report carries no Component for a zero amount, so these 25 fall out as Unreconciled payment under the definition. Left as it is: this is a classification question, not a config defect, and it is recorded here for the reviewer.
- Largest Reconciled-with-difference record_refs by absolute difference: none. No record_ref has a non-zero difference, so there is no top five to list.
- Tie-out figures are unchanged from ticket 01: Components 212,118.95 (difference 0.00), Released excluding Transfer 212,118.95 (difference 0.00), Deferred memo 43,540.07 over 2,398 lines.

Decisions made in this ticket:
- Consolidated Data follows the sample's columns, with `record_ref` and the payments and settlement source line numbers added at the right. Every unmapped line gets its own row, because it has no record_ref.
- A side's descriptive cells come from its earliest line (lowest line number). The settlement period, deposit date, currency and total-amount fields are blank on Component rows, so they stay blank.
- Per-Summary-line cells are blank for a Summary line with no field. Otherwise a side with lines shows its Contributions, with 0.00 where nothing routed.
- A routed Summary field with no Summary line (for example `sales_gift_wrap_credits`, or `bank_account_transfer_round_off` on the synthetic fixture) gets its own column on each side, after the Summary lines, and its own difference column. No Contribution leaves the Consolidated sheet.

Gaps, not closed in this ticket:
- The total difference is line-based, so the per-line differences do not add up to it wherever a routing gap exists.
- Consolidated values are stored numbers, not formulas.
- `WriteSource` deletes a report's rows across all runs, so only the latest run's lines are held. The run-level audit belongs to ticket 05.
