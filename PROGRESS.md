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
