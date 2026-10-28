-- MAPPING_FIXES.sql: fixes to the mapping configs, applied once to a freshly seeded config.
--
-- Applied by `recon apply-fixes --file MAPPING_FIXES.sql` (make after), straight after seed-config, in one
-- transaction. Every statement must change at least one row, so either every statement applies or none does.
-- Each UPDATE and DELETE is guarded by the old values it expects. A guard that matches no row (for example,
-- a second apply to the same config) fails the build loudly with the statement's number, and nothing is
-- committed. Every change is recorded in mapping_rule_history, and apply-fixes prints the rows each statement
-- changed.
--
-- Block format. One block per defect, in this shape:
--
--   -- Defect:          <short title>
--   -- Kind:            mapping defect | interpretation defect | schema limitation
--   -- Rule:            <rule ID, for example P-071>
--   -- Old value:       <field> = '<old>'
--   -- New value:       <field> = '<new>'
--   -- Why:             <the evidence: why the old value is wrong>
--   -- Variance closed: <Summary line, and its Variance before and after>
--   UPDATE payment_mapping_rules
--   SET <field> = '<new>'
--   WHERE rule_id = '<rule>' AND <field> = '<old>';
--
-- The three defect kinds:
--   mapping defect: a config row routes an amount to the wrong Summary field, or uses the wrong key.
--     Fixed here, with an UPDATE of that row, or a DELETE of a duplicate row.
--   interpretation defect: the matching logic gets a line wrong, for example a line matches no rule
--     because its description is written differently. Fixed by a named interpretation switch in
--     internal/mapping, not here. The block says which switch and has no UPDATE.
--   schema limitation: the config schema cannot express what the report needs, for example one tax
--     column that must route to two Summary fields. The block says why and has no UPDATE, because
--     data alone cannot close it.
--
-- Accounting-policy calls are not objective fixes. They go in the separate section at the end of this
-- file, with a recommendation and whether it was applied.

-- ===== Objective fixes =====

-- Defect:          Promotion discounts on orders routed to Expenses › Other, not Promo rebates
-- Kind:            mapping defect
-- Rule:            P-065
-- Old value:       to_summary_field_when_positive_amount = 'expenses_other', to_summary_field_when_negative_amount = 'expenses_other'
-- New value:       to_summary_field_when_positive_amount = 'expenses_promotional_rebates', to_summary_field_when_negative_amount = 'expenses_promotional_rebates'
-- Why:             The settlement report carries the same order promotions as ORDER / PROMOTION / PRINCIPAL (S-072) and
--                  ORDER / PROMOTION / SHIPPING (S-073), and both route to expenses_promotional_rebates. P-065 sends
--                  the payments promotional_rebates column for ORDER lines to expenses_other, which is the wrong bucket.
--                  The sibling REFUND promotional_rebates rule (P-017) routes to refunded_expenses, as the settlement
--                  refund promotions do. P-065 has 3,983 negative lines and no positive ones in this settlement.
-- Variance closed: Expenses › Promo rebates +11,273.53 → 0.00, and Expenses › Other -11,273.53 → 0.00 (applied first,
--                  so these are the values on the baseline; payments moves -11,273.53 from Other to Promo rebates).
UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'expenses_promotional_rebates',
    to_summary_field_when_negative_amount = 'expenses_promotional_rebates'
WHERE rule_id = 'P-065'
  AND to_summary_field_when_positive_amount = 'expenses_other'
  AND to_summary_field_when_negative_amount = 'expenses_other';

-- Defect:          Payments gift wrap credits routed to a Summary field that has no Summary line
-- Kind:            mapping defect
-- Rule:            P-070
-- Old value:       to_summary_field_when_positive_amount = 'sales_gift_wrap_credits', to_summary_field_when_negative_amount = 'sales_gift_wrap_credits'
-- New value:       to_summary_field_when_positive_amount = 'sales_other', to_summary_field_when_negative_amount = 'sales_other'
-- Why:             sales_gift_wrap_credits has no row in the Summary-line map, so the 11.61 is an unmapped field and
--                  never appears on a Summary line. The settlement report's gift wrap (ORDER / ITEMPRICE / GIFTWRAP,
--                  S-066, 11.61 on 3 lines) routes to sales_other. The payments side should use the same bucket. The
--                  three payments lines (3.99, 3.99 and 3.63) are all positive.
-- Variance closed: Sales › Other -11.97 → -0.36 (payments 0.00 → 11.61), and the unmapped sales_gift_wrap_credits
--                  field 11.61 → 0.00. The remaining -0.36 is the gift wrap tax, which the accounting policy closes.
UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_other',
    to_summary_field_when_negative_amount = 'sales_other'
WHERE rule_id = 'P-070'
  AND to_summary_field_when_positive_amount = 'sales_gift_wrap_credits'
  AND to_summary_field_when_negative_amount = 'sales_gift_wrap_credits';

-- Defect:          Duplicate payments rule: ORDER sales_tax_collected routed twice
-- Kind:            mapping defect
-- Rule:            P-072 (duplicate of P-071; deleted)
-- Old value:       transaction_type = 'ORDER', description = 'any', amount_field = 'sales_tax_collected', to_summary_field_when_positive_amount = 'sales_shipping' (row present)
-- New value:       row deleted
-- Why:             P-071 and P-072 both match every ORDER line and route the same sales_tax_collected column (13,675.59
--                  over 5,235 lines), P-071 to Product Charges and P-072 to Shipping. The Summary counts the tax twice.
--                  The duplicate is the one with the wrong field: the config has no reason to route one tax column to
--                  two lines, and the policy below sends the column to Sales › Tax. The DELETE is recorded in
--                  mapping_rule_history.
-- Variance closed: Sales › Shipping 13,572.17 → -103.42 (payments 21,773.13 → 8,097.54) after this block, on top of the
--                  blocks above. With P-005 (the next block) it is 93.20; together the two remove the 13,478.97 double count.
DELETE FROM payment_mapping_rules
WHERE rule_id = 'P-072'
  AND transaction_type = 'ORDER'
  AND description = 'any'
  AND amount_field = 'sales_tax_collected'
  AND to_summary_field_when_positive_amount = 'sales_shipping'
  AND to_summary_field_when_negative_amount = 'sales_shipping';

-- Defect:          Duplicate payments rule: ORDER low_value_goods routed twice
-- Kind:            mapping defect
-- Rule:            P-005 (duplicate of P-006; deleted)
-- Old value:       transaction_type = 'ORDER', description = 'any', amount_field = 'low_value_goods', to_summary_field_when_positive_amount = 'sales_shipping' (row present)
-- New value:       row deleted
-- Why:             P-005 and P-006 both match every ORDER line and route the same low_value_goods column (-196.62 over 50
--                  lines), P-005 to Shipping and P-006 to Product Charges. The Summary counts it twice. P-006 stays as
--                  the one row that carries the column; its Summary field is set by the policy below.
-- Variance closed: Sales › Shipping -103.42 → 93.20 (payments 8,097.54 → 8,294.16, the -196.62 double count removed).
--                  With the block above, Sales › Shipping goes 13,572.17 → 93.20 in total; the policy below closes the 93.20.
DELETE FROM payment_mapping_rules
WHERE rule_id = 'P-005'
  AND transaction_type = 'ORDER'
  AND description = 'any'
  AND amount_field = 'low_value_goods'
  AND to_summary_field_when_positive_amount = 'sales_shipping'
  AND to_summary_field_when_negative_amount = 'sales_shipping';

-- ===== Interpretation defect (fixed in the matching logic, not in this file) =====

-- Defect:          The Transfer line is not matched by its rule, so it falls to the Catch-all and is summarised as a fee
-- Kind:            interpretation defect
-- Rule:            P-063 (TRANSFER / TO_ACCOUNT_ENDING, total), which has no Summary field
-- Old value:       n/a (the config row is correct as written; the description carries account digits)
-- New value:       n/a (matching logic: the switch prefix_before_catch_all, in internal/mapping/switches.go and mapper.go)
-- Why:             The real Transfer description is "To account ending with: <3 digits>". The config author wrote the
--                  rule's description as the prefix "TO_ACCOUNT_ENDING", which is correct for the bank-transfer shape,
--                  but an exact match cannot read it. Without the switch the line falls to the Catch-all P-110, and its
--                  total (-133,756.51) is routed to Expenses › Amazon fees. The switch lets a rule of the same
--                  transaction type match a line whose description starts with the rule's description followed by "_"
--                  or ":", after the exact and wildcard rules and before the Catch-all. It is on in a fixed run and off
--                  in a baseline run, which is recorded on the run. Seam-2 tests cover both modes.
-- Variance closed: Expenses › Amazon fees -133,756.51 → 0.00 (payments -266,349.92 → -132,593.41, the same as the settlements).
--                  The Transfer is then an Unreconciled payment with no Summary contribution, as its rule intends, and it
--                  is counted in the Payments Tie-out routing gap.

-- ===== Schema limitation (no SQL; the config schema cannot express this) =====

-- Defect:          One payments tax column must split across two Summary lines, and the payments config cannot split it
-- Kind:            schema limitation
-- Rule:            P-071 (ORDER sales_tax_collected) and P-006 (ORDER low_value_goods) on the payments side; S-065, S-063,
--                  S-095, S-061 (tax components) and S-119, S-118 (low-value goods components) on the settlement side
-- Old value:       n/a
-- New value:       n/a
-- Why:             Payments has one column for the tax on ORDER lines (13,675.59). Settlement splits the same money into
--                  four components: ITEMPRICE/TAX 13,765.71 (Product Charges), ITEMPRICE/SHIPPINGTAX 581.68 (Shipping),
--                  PROMOTION/TAXDISCOUNT -672.16 (Shipping) and ITEMPRICE/GIFTWRAPTAX 0.36 (Other). The low-value goods
--                  column (-196.62) splits into two components too (-193.90 principal, -2.72 shipping). A payments rule
--                  has one amount column and two Summary fields by sign, not one by component, so no row can route the
--                  column by component. The tax is in the wrong lines, not missing: the Sales lines net to 0.00 once
--                  the duplicates are removed.
-- Schema change:   Either (a) a share table, amount_field -> (summary_field, share), applied to the column during
--                  ingestion, so that a payments column can be split in the proportions the settlement shows; the
--                  shares must come from the settlement components of the same record_ref, which makes this a
--                  reconciliation rule rather than a config row; or (b) a Summary line that holds all sales tax, which
--                  is the accounting policy below. The policy is the only one applied here, because (a) needs
--                  per-record shares that the payments report does not carry.
-- Variance closed: none by itself. After the blocks above it leaves Sales › Product Charges -92.84, Shipping 93.20 and
--                  Other -0.36, which net to 0.00 across the Sales lines. The accounting policy below closes them.

-- ===== Accounting-policy calls (separate; not objective fixes; each one needs a reviewer decision) =====

-- Defect:          Sales tax and refund tax split across Summary lines the payments config cannot split
-- Kind:            accounting-policy call (applied; reviewer approved 2026-10-28)
-- Recommendation:  Route every tax component to Sales › Tax (sales_tax, the sample's Tax line, which has no rule in the
--                  baseline). This keeps the tax out of Product Charges, Shipping, Other and Refund expenses, where it
--                  distorts the Sales and Refunds lines, and it matches the Summary's own Tax line.
-- Rule:            payments P-071, P-006, P-014; settlement S-065, S-063, S-095, S-061, S-119, S-118, S-004, S-011, S-020
-- Old value:       P-071 sales_product_charges (both signs); P-006 sales_product_charges (both signs); P-014 empty (both
--                  signs); S-065 sales_product_charges (both); S-063 sales_shipping (both); S-095 sales_shipping (both);
--                  S-061 sales_other (both); S-119 sales_product_charges (both); S-118 sales_shipping (both); S-004
--                  total_refund_expense_or_sales_amt (positive) and refunded_expenses (negative); S-011 refunded_expenses
--                  (both); S-020 refunded_expenses (both)
-- New value:       sales_tax on both signs, for every rule above
-- Why:             Payments has one tax column, so it cannot be split (see the schema limitation above). Routing every
--                  tax component to the same Summary line lets the two sides agree at that line. It applies to both
--                  signs: the refund tax is negative on the payments side and in the settlement components. Among the
--                  rules routed here, S-004 has no positive lines in this settlement. P-014 is the refund tax on the
--                  payments side, which the Refund expenses line never routed; the settlement refund tax moves out of
--                  Refund expenses with it, so both sides agree there.
-- Variance closed: Sales › Tax: both sides 0.00 → 13,436.38, Variance 0.00 throughout;
--                  Sales › Product Charges -92.84 → 0.00 (payments 348,815.93 → 335,336.96);
--                  Sales › Shipping 93.20 → 0.00 (payments 8,294.16 on both sides);
--                  Sales › Other -0.36 → 0.00 (payments 11.61 on both sides);
--                  Refunds › Refund expenses 42.59 → 0.00 (payments 258.87 on both sides).
-- Applied:         yes
UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'P-071' AND transaction_type = 'ORDER' AND amount_field = 'sales_tax_collected'
  AND to_summary_field_when_positive_amount = 'sales_product_charges'
  AND to_summary_field_when_negative_amount = 'sales_product_charges';

UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'P-006' AND transaction_type = 'ORDER' AND amount_field = 'low_value_goods'
  AND to_summary_field_when_positive_amount = 'sales_product_charges'
  AND to_summary_field_when_negative_amount = 'sales_product_charges';

UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'P-014' AND transaction_type = 'REFUND' AND amount_field = 'sales_tax_collected'
  AND to_summary_field_when_positive_amount = ''
  AND to_summary_field_when_negative_amount = '';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'S-065' AND transaction_type = 'ORDER' AND amount_type = 'ITEMPRICE' AND amount_description = 'TAX'
  AND to_summary_field_when_positive_amount = 'sales_product_charges'
  AND to_summary_field_when_negative_amount = 'sales_product_charges';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'S-063' AND transaction_type = 'ORDER' AND amount_type = 'ITEMPRICE' AND amount_description = 'SHIPPINGTAX'
  AND to_summary_field_when_positive_amount = 'sales_shipping'
  AND to_summary_field_when_negative_amount = 'sales_shipping';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'S-095' AND transaction_type = 'ORDER' AND amount_type = 'PROMOTION' AND amount_description = 'TAXDISCOUNT'
  AND to_summary_field_when_positive_amount = 'sales_shipping'
  AND to_summary_field_when_negative_amount = 'sales_shipping';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'S-061' AND transaction_type = 'ORDER' AND amount_type = 'ITEMPRICE' AND amount_description = 'GIFTWRAPTAX'
  AND to_summary_field_when_positive_amount = 'sales_other'
  AND to_summary_field_when_negative_amount = 'sales_other';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'S-119' AND transaction_type = 'ORDER' AND amount_type = 'ITEMWITHHELDTAX' AND amount_description = 'LOWVALUEGOODSTAX-PRINCIPAL'
  AND to_summary_field_when_positive_amount = 'sales_product_charges'
  AND to_summary_field_when_negative_amount = 'sales_product_charges';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'S-118' AND transaction_type = 'ORDER' AND amount_type = 'ITEMWITHHELDTAX' AND amount_description = 'LOWVALUEGOODSTAX-SHIPPING'
  AND to_summary_field_when_positive_amount = 'sales_shipping'
  AND to_summary_field_when_negative_amount = 'sales_shipping';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'S-004' AND transaction_type = 'REFUND' AND amount_type = 'ITEMPRICE' AND amount_description = 'TAX'
  AND to_summary_field_when_positive_amount = 'total_refund_expense_or_sales_amt'
  AND to_summary_field_when_negative_amount = 'refunded_expenses';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'S-011' AND transaction_type = 'REFUND' AND amount_type = 'ITEMPRICE' AND amount_description = 'SHIPPINGTAX'
  AND to_summary_field_when_positive_amount = 'refunded_expenses'
  AND to_summary_field_when_negative_amount = 'refunded_expenses';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'S-020' AND transaction_type = 'REFUND' AND amount_type = 'PROMOTION' AND amount_description = 'TAXDISCOUNT'
  AND to_summary_field_when_positive_amount = 'refunded_expenses'
  AND to_summary_field_when_negative_amount = 'refunded_expenses';
