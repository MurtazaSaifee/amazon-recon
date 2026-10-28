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

## 2026-10-28: Ticket 06, investigation tooling and before/after builds

Built:
- Variance views (migration 0005): `v_line_variance` (Summary line × source × rule, with the rule's label, Amount field, line count and amount) and `v_record_variance` (record_ref × Summary line, payments, settlements and difference).
- `recon explain --line "<group>/<line>"` reads those views. It prints the Variance, the Contributions by rule on each side, the top 10 record_refs by absolute difference, and their source line numbers.
- Interpretation-switch registry (`internal/mapping`). It is empty for now, so the fixed and baseline runs currently have the same switch set (none). `ingest --baseline` turns every switch off, and each run records its switch names and mode. The report header prints both.
- `MAPPING_FIXES.sql` (empty, with the block format and a separate accounting-policy section), applied by `recon apply-fixes` in one transaction.
- `make before` and `make after` each migrate `--reset`, seed the configs, and write `reports/before.xlsx` or `reports/after.xlsx`. `make after` applies `MAPPING_FIXES.sql` first.
- Summary formula cells now cache a number rather than text. excelize writes `t="str"` on every formula, so the workbook is rewritten after save to drop it.

Real files, `make before` and `make after` (both from scratch on database `recon_t06`):
- Both builds reconcile the same: Components 212,118.95 (difference 0.00), 13,289 reconciled record_refs, 26 unreconciled payments, Transfer -133,756.51.
- The two builds are identical because no fix exists yet. The after build will differ once fixes are added.

`recon explain --line "Expenses/Amazon fees"` on the baseline (trimmed):

```
Expenses / Amazon fees  (Summary field expenses_amazon_fees, run 1)

Payments     -266,349.92
Settlements  -132,593.41
Variance     -133,756.51

Contributions by rule
Side         Rule   Transaction type / description    Amount field  Amount       Lines
Payments     P-110  (catch-all) / any                 total         -133,756.51  1
...
Top record_refs by absolute difference (1 shown)
1. |90000000001|2026-07-18
   difference -133,756.51 (payments -133,756.51, settlements 0.00)
   payments lines:    amazon_payments_data.csv line 16605
   settlement lines:  none
```

The Variance is the Transfer line (payments line 16605, release 18 Jul, record_ref with an empty order ID), falling to the Catch-all rule P-110. The rule-level detail shows the other Expenses contributions too (P-064, P-073, P-100 on payments; S-025, S-067 to S-070 on settlements). Those are for later investigation.

Decisions made in this ticket:
- The Catch-all's blank transaction type is labelled `(catch-all)` in the views and the explain output.
- `make before` and `make after` use `--reset`, so each run starts from an empty database. Both need the database name to start with `recon`.

Gaps, not closed in this ticket:
- The switch registry is empty, so the `--baseline` and fixed runs cannot yet differ. A test with a registered switch would show that they do; for now the switch set is only checked as empty.
- The Catch-all label is tested on the real files only. The synthetic fixtures never fire P-010 or any other Catch-all rule.
- No mapping fixes exist yet, so the after build equals the before build.

## 2026-10-28: Ticket 07, Step 5 pass A: the Variances on the Baseline (before any fix)

Starting point: `make before` from scratch on database `recon_t07`, reading the real files from `INPUT_DIR` (nothing copied into the repo). Run 1, Settlement 90000000001, mode baseline, interpretation switches none, config hash a600169ca2fe, 0 malformed lines. The counts are the same as ticket 06: 13,289 Reconciled, 0 Reconciled with difference, 26 Unreconciled payment, 0 Unreconciled settlement.

### Every Variance on the Baseline

Variance is Payments minus Settlements. Eight lines are non-zero: seven Summary lines, and one Summary field that has no Summary line.

| Summary line | Payments | Settlements | Variance |
|---|---:|---:|---:|
| Sales › Product Charges | 348,815.93 | 348,908.77 | -92.84 |
| Sales › Shipping | 21,773.13 | 8,200.96 | 13,572.17 |
| Sales › Other | 0.00 | 11.97 | -11.97 |
| Refunds › Refund expenses | 258.87 | 216.28 | 42.59 |
| Expenses › Promo rebates | 0.00 | -11,273.53 | 11,273.53 |
| Expenses › Other | -11,273.53 | 0.00 | -11,273.53 |
| Expenses › Amazon fees | -266,349.92 | -132,593.41 | -133,756.51 |
| `sales_gift_wrap_credits` (no Summary line) | 11.61 | 0.00 | 11.61 |

Zero Variance: Sales › Tax (0.00 on both sides), Sales › Inventory Reimbursements (679.69), Refunds › Refunded sales (-2,031.37) and Expenses › FBA fees (-0.41). Sales › Tax is zero because the only rules that name it (P-040 and S-075, COMMINGLING_VAT) have no in-scope lines, so every tax amount in this settlement lands in Product Charges, Shipping or Other. The subtotals (Sales 13,467.36, Refunds 42.59, Expenses -133,756.51) are sums of the lines above, so I did not trace them separately.

The Payments Tie-out on this run: Summary net 91,884.00, routing gaps -13,521.56, in-scope total 78,362.44, remaining difference to the Settlement total -133,756.51 (the Transfer). The Settlements Tie-out is 0.00.

### 1. Expenses › Amazon fees: -133,756.51

`recon explain --line "Expenses/Amazon fees"` (trimmed):

```
Expenses / Amazon fees  (expenses_amazon_fees, run 1)
Payments     -266,349.92
Settlements  -132,593.41
Variance     -133,756.51

Side         Rule   Transaction type / description                             Amount field  Amount       Lines
Payments     P-064  ORDER / any                                                selling_fees  -35,773.27   10855
Payments     P-073  ORDER / any                                                fba_fees      -96,803.17   13328
Payments     P-100  SERVICE_FEE / SUBSCRIPTION                                 other         -16.97       1
Payments     P-110  (catch-all) / any                                          total         -133,756.51  1
Settlements  S-025  OTHER-TRANSACTION / OTHER-TRANSACTION / SUBSCRIPTION_FEE   amount        -16.97       1
Settlements  S-067  ORDER / ITEMFEES / FBAPERUNITFULFILLMENTFEE                 amount        -95,592.26   13328
Settlements  S-068  ORDER / ITEMFEES / GIFTWRAPCHARGEBACK                       amount        -11.97       3
Settlements  S-069  ORDER / ITEMFEES / SHIPPINGCHARGEBACK                        amount        -1,198.94    250
Settlements  S-070  ORDER / ITEMFEES / COMMISSION                               amount        -35,773.27   10855

Top record_refs by absolute difference (1 shown)
1. |90000000001|2026-07-18   difference -133,756.51 (payments -133,756.51, settlements 0.00)
   payments lines: amazon_payments_data.csv line 16605
```

- Root cause: the Transfer line (payments line 16605, released 18 Jul 2026, description "To account ending with: nnn", total -133,756.51) is the payout to the bank account, not a fee. Its description carries account digits, so it does not match `TRANSFER / TO_ACCOUNT_ENDING` (P-063) exactly. It falls to the Catch-all wildcard P-110 (`(catch-all) / any / total`), which routes `total` to Amazon fees. P-063 has no Summary field, so once matched the Transfer is deliberately not summarised. The config author wrote the description as a prefix of the real text, so the defect is in how the matcher reads the description, not in the config row.
- Rule IDs: P-110 (matched, catch-all wildcard, tier 4). P-063 (should match, does not). P-062 (`TO_YOUR_ACCOUNT_ENDING`) has the same shape, and no line in this settlement matches it.
- Evidence sums: payments Amazon fees -266,349.92 = P-064 -35,773.27 + P-073 -96,803.17 + P-100 -16.97 + P-110 -133,756.51. Settlements -132,593.41 = S-025 -16.97 + S-067 -95,592.26 + S-068 -11.97 + S-069 -1,198.94 + S-070 -35,773.27. Without P-110 the payments side is -132,593.41, the same as the settlements side, to the cent.
- Hypotheses ruled out: a second Transfer in scope (payments line 1396, -97,919.76, 4 Jul, belongs to settlement 90000000003 and is out of scope); a parsing error (the amount, with its thousands separator, parses to exactly -133,756.51); a sign error (a Transfer is money out, and P-063 has no sign-dependent field).
- Fix level: interpretation defect. A named switch, `prefix_before_catch_all`, lets a rule of the same transaction type match when its description is a prefix of the line's description, before the Catch-all is tried. P-063 stays as it is.

### 2. Expenses › Promo rebates: +11,273.53 and Expenses › Other: -11,273.53

One cause for both lines. `recon explain --line "Expenses/Other"` (trimmed):

```
Expenses / Other  (expenses_other, run 1)
Payments     -11,273.53
Settlements  0.00
Variance     -11,273.53

Side         Rule   Transaction type / description  Amount field         Amount       Lines
Payments     P-065  ORDER / any                     promotional_rebates  -11,273.53   3983
```

`recon explain --line "Expenses/Promo rebates"` (trimmed):

```
Expenses / Promo rebates  (expenses_promotional_rebates, run 1)
Payments     0.00
Settlements  -11,273.53
Variance     11,273.53

Side         Rule   Transaction type / description  Amount field  Amount     Lines
Settlements  S-072  ORDER / PROMOTION / PRINCIPAL    amount        -4,098.30  1580
Settlements  S-073  ORDER / PROMOTION / SHIPPING     amount        -7,175.23  2613
```

- Root cause: P-065 (`ORDER / any / promotional_rebates`) sends both signs to `expenses_other`. The settlement report carries the same order promotions as `ORDER / PROMOTION / PRINCIPAL` (S-072) and `ORDER / PROMOTION / SHIPPING` (S-073), and those route to `expenses_promotional_rebates`. The Summary field in P-065 is wrong. This is a mapping defect.
- Rule IDs: P-065 (payments); S-072 and S-073 (settlements). The sibling P-017 (`REFUND / any / promotional_rebates`) routes to `refunded_expenses`, and the settlement refund promotions (S-021, S-022) route there too, so the two sides agree on refunds.
- Evidence sums: P-065 is -11,273.53 over 3,983 lines, all negative. S-072 -4,098.30 + S-073 -7,175.23 = -11,273.53. The totals agree to the cent.
- Hypotheses ruled out: a sign problem (P-065 has no positive lines, and both settlement rules have only negative lines); a matching problem (the records reconcile on totals, so only the Summary bucket differs).
- Fix: F1, a mapping defect in P-065.

### 3. Sales › Shipping: +13,572.17, Sales › Product Charges: -92.84, Sales › Other: -11.97

`recon explain --line "Sales/Shipping"` (trimmed):

```
Sales / Shipping  (sales_shipping, run 1)
Payments     21,773.13
Settlements  8,200.96
Variance     13,572.17

Side         Rule   Transaction type / description                        Amount field         Amount      Lines
Payments     P-005  ORDER / any                                          low_value_goods      -196.62     50
Payments     P-068  ORDER / any                                          shipping_credits     8,294.16    2864
Payments     P-072  ORDER / any                                          sales_tax_collected  13,675.59   5235
Settlements  S-062  ORDER / ITEMPRICE / SHIPPING                         amount               8,294.16    2864
Settlements  S-063  ORDER / ITEMPRICE / SHIPPINGTAX                      amount               581.68      2013
Settlements  S-095  ORDER / PROMOTION / TAXDISCOUNT                      amount               -672.16     2506
Settlements  S-118  ORDER / ITEMWITHHELDTAX / LOWVALUEGOODSTAX-SHIPPING  amount               -2.72       3
```

`recon explain --line "Sales/Product Charges"` (trimmed):

```
Sales / Product Charges  (sales_product_charges, run 1)
Payments     348,815.93
Settlements  348,908.77
Variance     -92.84

Side         Rule   Transaction type / description                         Amount field         Amount      Lines
Payments     P-006  ORDER / any                                           low_value_goods      -196.62     50
Payments     P-067  ORDER / any                                           product_sales        335,336.96  13328
Payments     P-071  ORDER / any                                           sales_tax_collected  13,675.59   5235
Settlements  S-064  ORDER / ITEMPRICE / PRINCIPAL                         amount               335,336.96  13328
Settlements  S-065  ORDER / ITEMPRICE / TAX                               amount               13,765.71   5235
Settlements  S-119  ORDER / ITEMWITHHELDTAX / LOWVALUEGOODSTAX-PRINCIPAL  amount               -193.90     50
```

`recon explain` has no Summary line to show for `sales_gift_wrap_credits`, so its trace is the query on the payments lines below, and the Sales › Other trace is the Settlements side of the same explain output (S-061 and S-066, with nothing on the Payments side).

- Root cause A, duplicated payments rules (Multi-rule hit). P-071 and P-072 both route ORDER `sales_tax_collected` (13,675.59, 5,235 lines each), one to Product Charges and one to Shipping. P-005 and P-006 both route ORDER `low_value_goods` (-196.62, 50 lines each), one to Shipping and one to Product Charges. Each duplicate adds the same amount to a second Summary field, so the Summary counts it twice. The net double count is 13,675.59 - 196.62 = 13,478.97. This is a mapping defect: the duplicate rows must go.
- Root cause B, one tax column against four settlement components (schema limitation, with a policy decision). The payments `sales_tax_collected` for ORDER lines is 13,675.59. Settlements carry the same money as S-065 ITEMPRICE/TAX 13,765.71, S-063 ITEMPRICE/SHIPPINGTAX 581.68, S-095 PROMOTION/TAXDISCOUNT -672.16 and S-061 ITEMPRICE/GIFTWRAPTAX 0.36. Payments `low_value_goods` (-196.62) is S-119 LOWVALUEGOODSTAX-PRINCIPAL -193.90 plus S-118 LOWVALUEGOODSTAX-SHIPPING -2.72. The payments config has one row per amount column, so one column cannot be sent to two Summary fields. The tax is in the wrong lines, not missing. Once root cause A is removed, the residuals are Product Charges -92.84 (tax -90.12, low-value goods -2.72), Shipping +93.20 and Other -0.36, which net to 0.00 across the Sales lines.
- Root cause C, gift wrap (mapping defect). P-070 (`ORDER / any / gift_wrap_credits`) routes to `sales_gift_wrap_credits`, a field with no Summary line. Settlement ITEMPRICE/GIFTWRAP (S-066, 11.61 on 3 lines) routes to `sales_other`. The payments side is 11.61 on lines 5955, 14054 and 14269 (3.99, 3.99 and 3.63), and it appears in the Summary only as an unmapped field. This is what makes the `sales_gift_wrap_credits` line non-zero, and it is the 11.61 of the Sales › Other Variance. The remaining -0.36 of Sales › Other is S-061 GIFTWRAPTAX, which is root cause B.
- Rule IDs: P-005, P-006, P-071, P-072, P-070 (payments); S-061, S-063, S-065, S-066, S-095, S-118, S-119 (settlements).
- Evidence sums: payments Shipping 21,773.13 = P-068 8,294.16 + P-072 13,675.59 + P-005 -196.62. Settlements Shipping 8,200.96 = S-062 8,294.16 + S-063 581.68 + S-095 -672.16 + S-118 -2.72. Payments Product Charges 348,815.93 = P-067 335,336.96 + P-071 13,675.59 + P-006 -196.62. Settlements 348,908.77 = S-064 335,336.96 + S-065 13,765.71 + S-119 -193.90.
- Hypotheses ruled out: a sign problem in the tax rules (every P-071 and P-072 line is positive, every S-065 and S-063 line is positive, and S-095 is negative); a key problem (the ORDER records reconcile on totals, so the difference is in routing only).
- Fix: F3 (delete P-072), F4 (delete P-005), F2 (P-070 to `sales_other`), and the tax policy (route all tax to Sales › Tax, see the accounting-policy section of MAPPING_FIXES.sql).

### 4. Refunds › Refund expenses: +42.59

`recon explain --line "Refunds/Refund expenses"` (trimmed):

```
Refunds / Refund expenses  (refunded_expenses, run 1)
Payments     258.87
Settlements  216.28
Variance     42.59

Side         Rule   Transaction type / description          Amount field         Amount     Lines
Payments     P-011  REFUND / any                            fba_fees             28.89      6
Payments     P-017  REFUND / any                            promotional_rebates  112.62     33
Payments     P-018  REFUND / any                            selling_fees         117.36     42
Settlements  S-004  REFUND / ITEMPRICE / TAX                amount               -41.09     13
Settlements  S-011  REFUND / ITEMPRICE / SHIPPINGTAX        amount               -3.19      11
Settlements  S-016  REFUND / ITEMFEES / SHIPPINGCHARGEBACK  amount               28.89      6
Settlements  S-017  REFUND / ITEMFEES / COMMISSION          amount               146.62     42
Settlements  S-018  REFUND / ITEMFEES / REFUNDCOMMISSION    amount               -29.26     42
Settlements  S-020  REFUND / PROMOTION / TAXDISCOUNT        amount               1.69       8
Settlements  S-021  REFUND / PROMOTION / PRINCIPAL          amount               64.76      19
Settlements  S-022  REFUND / PROMOTION / SHIPPING           amount               47.86      19
```

- Root cause: P-014 (`REFUND / any / sales_tax_collected`) has no Summary field in either direction. Its in-scope lines carry refund tax of -42.59 (16 non-zero lines, out of 73 in-scope refunds). On the settlement side the refund tax components route to `refunded_expenses`: S-004 -41.09, S-011 -3.19 and S-020 +1.69, a total of -42.59. The payments Refund expenses line leaves out exactly the amount the settlements include, so the Variance is +42.59.
- Rule IDs: P-014 (payments, empty Summary field); S-004, S-011, S-020 (settlements, `refunded_expenses`).
- Evidence sums: -41.09 - 3.19 + 1.69 = -42.59, which equals the in-scope P-014 total. No S-004 line is positive (13 negative, 0 positive), so nothing goes to the unmapped `total_refund_expense_or_sales_amt`.
- Hypotheses ruled out: a settlement-side fee problem (the non-tax refund rows agree with the payments refund fees to the cent: P-011 28.89 = S-016 28.89; P-018 117.36 = S-017 146.62 + S-018 -29.26; P-017 112.62 = S-021 64.76 + S-022 47.86); a sign problem (the S-020 positive lines, 8 of them, route to `refunded_expenses` on either sign).
- Fix: P-014 has no fix of its own. It is closed by the tax policy (refund tax to Sales › Tax, both sides), which the reviewer approved on 2026-10-28. The decision is in the accounting-policy section of MAPPING_FIXES.sql.

### Hypotheses ruled out for the whole run

- The record_ref key or the posted-date basis: every in-scope record_ref that exists on both sides reconciles to the cent (13,289 Reconciled, 0 Reconciled with difference). The Variances are routing differences, not matching differences.
- Scope: the Settlements Tie-out is 0.00, and the Released lines excluding the Transfer total 212,118.95, the same as the Settlement header.
- Thousands separators: the Transfer amount parses to the value the file shows. I checked this on the Transfer line, the only amount with a separator among the Variance lines.
- Sign routing: every rule behind a Variance has one sign only in this settlement (P-005, P-006, P-065, P-071, P-072, S-004, S-011, S-061, S-063, S-065, S-066, S-072, S-073, S-095, S-118, S-119 and S-020, checked rule by rule), so the choice between the positive and negative Summary field does not explain any Variance.

### Decisions in this pass

- Objective mapping fixes F1 to F4 go in MAPPING_FIXES.sql, one block each, guarded by their old values.
- The Transfer goes to the matching logic, as the switch `prefix_before_catch_all`, with seam-2 tests in both modes.
- The tax split is a schema limitation, documented in MAPPING_FIXES.sql. Routing all tax to Sales › Tax (both signs) is an accounting-policy call, applied in its own section after the reviewer's approval on 2026-10-28.
- Every fix is confirmed by the reviewer before it is applied. The approvals were given on 2026-10-28.
