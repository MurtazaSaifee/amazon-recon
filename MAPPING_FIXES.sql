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

-- ===== Objective fixes, pass B (semantic audit of every rule that fires; ticket 08; approved 2026-10-28) =====

-- Defect:          [pass B, O1] Negative side of an "other" adjustment rule is blank, so negative corrections are dropped
-- Kind:            mapping defect
-- Rule:            P-031 (ADJUSTMENT FBA_INVENTORY_REIMBURSEMENT_-_FEE_CORRECTION, other)
-- Old value:       to_summary_field_when_negative_amount = ''
-- New value:       to_summary_field_when_negative_amount = 'expenses_other'
-- Why:             The positive side routes to sales_other and the negative side is blank, so a negative fee correction
--                  reaches no Summary line. P-031 is the only "other" adjustment rule with a blank non-total side. Its
--                  siblings route both signs: P-108 (shipping adjustment) sends positive to sales_other and negative to
--                  expenses_other. The settlement has no counterpart component, so a negative correction would show as a
--                  Variance if one arrived. P-031 has no in-scope line in this settlement, so this moves no money here.
-- Variance closed: none. Sales › Other 11.61 → 11.61 and Expenses › Other 0.00 → 0.00 (no line fires P-031).
UPDATE payment_mapping_rules
SET to_summary_field_when_negative_amount = 'expenses_other'
WHERE rule_id = 'P-031' AND amount_field = 'other'
  AND to_summary_field_when_positive_amount = 'sales_other'
  AND to_summary_field_when_negative_amount = '';

-- Defect:          [pass B, O2] Refund and low-value-goods tax rules left outside the sales tax route
-- Kind:            mapping defect
-- Rule:            P-004 (REFUND low_value_goods); S-012 (REFUND ITEMPRICE GIFTWRAPTAX); S-127 (ORDER ITEMWITHHELDTAX LOWVALUEGOODSTAX-OTHER)
-- Old value:       P-004 both signs ''; S-012 both signs refunded_expenses; S-127 both signs sales_other
-- New value:       P-004, S-012 and S-127, both signs: sales_tax
-- Why:             The accounting policy routes all sales tax to Sales › Tax (the policy block below). These three rules
--                  carry tax that their siblings already route there: P-006 and P-014 (payments tax), S-004 and S-011 (refund
--                  tax), and S-118 and S-119 (low-value-goods tax). Left as they are, the same tax lands on two or three
--                  lines, and P-004 is blank, so its refund tax reaches no line on the payments side. P-004 has 73 matched
--                  lines, none of them non-zero in this settlement. S-012 and S-127 have no in-scope line.
-- Variance closed: none. Sales › Tax 13,436.38 → 13,436.38 (both sides); no line had a Variance before or after.
UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'P-004' AND transaction_type = 'REFUND' AND amount_field = 'low_value_goods'
  AND to_summary_field_when_positive_amount = ''
  AND to_summary_field_when_negative_amount = '';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'S-012' AND transaction_type = 'REFUND' AND amount_type = 'ITEMPRICE' AND amount_description = 'GIFTWRAPTAX'
  AND to_summary_field_when_positive_amount = 'refunded_expenses'
  AND to_summary_field_when_negative_amount = 'refunded_expenses';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'S-127' AND transaction_type = 'ORDER' AND amount_type = 'ITEMWITHHELDTAX' AND amount_description = 'LOWVALUEGOODSTAX-OTHER'
  AND to_summary_field_when_positive_amount = 'sales_other'
  AND to_summary_field_when_negative_amount = 'sales_other';

-- Defect:          [pass B, O3] SAFE-T reimbursement routed to an expense line, and one settlement side with no sign split
-- Kind:            mapping defect
-- Rule:            P-059 (SAFE-T_REIMBURSEMENT, other); S-079 (SAFE-T_REIMBURSEMENT, OTHER_TRANSACTIONS)
-- Old value:       P-059 both signs expenses_fba_fees; S-079 both signs sales_inventory_reimbursements
-- New value:       P-059 and S-079: positive sales_inventory_reimbursements, negative expenses_reversed_reimbursements
-- Why:             A SAFE-T reimbursement is money paid to the seller, so it belongs with the reimbursements, not with the
--                  FBA fees. Every other FBA reimbursement routes positive to Sales › Inventory Reimbursements and negative
--                  to Expenses › Reversed Reimbursements (the sign split of P-024 to P-036 and S-050 to S-059). P-059 sent
--                  both signs to an expense line while S-079 sent both to Inventory Reimbursements, so the two sides put the
--                  same component on different lines. SAFE-T is absent from both files in this run, so this moves no money here.
-- Variance closed: none. Sales › Inventory Reimbursements 679.69 → 679.69 and Expenses › FBA fees -0.41 → -0.41 (no lines).
UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_inventory_reimbursements',
    to_summary_field_when_negative_amount = 'expenses_reversed_reimbursements'
WHERE rule_id = 'P-059' AND transaction_type = 'SAFE-T_REIMBURSEMENT' AND amount_field = 'other'
  AND to_summary_field_when_positive_amount = 'expenses_fba_fees'
  AND to_summary_field_when_negative_amount = 'expenses_fba_fees';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_inventory_reimbursements',
    to_summary_field_when_negative_amount = 'expenses_reversed_reimbursements'
WHERE rule_id = 'S-079' AND transaction_type = 'SAFE-T_REIMBURSEMENT' AND amount_type = 'OTHER_TRANSACTIONS'
  AND amount_description = 'SAFE-T_REIMBURSEMENT'
  AND to_summary_field_when_positive_amount = 'sales_inventory_reimbursements'
  AND to_summary_field_when_negative_amount = 'sales_inventory_reimbursements';

-- Defect:          [pass B, O4] Refund gift wrap, goodwill, restocking and other routed to a field with no Summary line
-- Kind:            mapping defect
-- Rule:            P-015 and P-016 (payments REFUND); S-005, S-006 and S-007 (settlement REFUND ITEMPRICE)
-- Old value:       P-015, P-016, S-005, S-006 and S-007, both signs: total_refund_expense_or_sales_amt
-- New value:       P-015, S-005 and S-006, both signs: refunded_sales; P-016 and S-007, both signs: refunded_expenses
-- Why:             total_refund_expense_or_sales_amt has no row in the Summary-line map, so these amounts reach no Summary
--                  line on either side, and the Variance check cannot see them. Gift wrap and goodwill reduce the refund's
--                  sale, so they go to Refunded sales, as the refund product sales do (P-009, S-013). Restocking and other
--                  refund amounts are costs of the refund, so they go to Refund expenses, with the refund fees (P-011, P-018,
--                  S-016 to S-018). These are the recommended targets; a reviewer may choose other lines. The payments
--                  "other" column does not name goodwill, so P-016 and S-006 are not a matched pair, and the split between
--                  them is a judgement. None of the five rules has an in-scope line in this settlement.
-- Variance closed: none. Refunds › Refunded sales -2,031.37 → -2,031.37 and Refunds › Refund expenses 258.87 → 258.87 (no lines).
UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'refunded_sales',
    to_summary_field_when_negative_amount = 'refunded_sales'
WHERE rule_id = 'P-015' AND transaction_type = 'REFUND' AND amount_field = 'gift_wrap_credits'
  AND to_summary_field_when_positive_amount = 'total_refund_expense_or_sales_amt'
  AND to_summary_field_when_negative_amount = 'total_refund_expense_or_sales_amt';

UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'refunded_expenses',
    to_summary_field_when_negative_amount = 'refunded_expenses'
WHERE rule_id = 'P-016' AND transaction_type = 'REFUND' AND amount_field = 'other'
  AND to_summary_field_when_positive_amount = 'total_refund_expense_or_sales_amt'
  AND to_summary_field_when_negative_amount = 'total_refund_expense_or_sales_amt';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'refunded_sales',
    to_summary_field_when_negative_amount = 'refunded_sales'
WHERE rule_id IN ('S-005', 'S-006') AND transaction_type = 'REFUND' AND amount_type = 'ITEMPRICE'
  AND to_summary_field_when_positive_amount = 'total_refund_expense_or_sales_amt'
  AND to_summary_field_when_negative_amount = 'total_refund_expense_or_sales_amt';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'refunded_expenses',
    to_summary_field_when_negative_amount = 'refunded_expenses'
WHERE rule_id = 'S-007' AND transaction_type = 'REFUND' AND amount_type = 'ITEMPRICE'
  AND amount_description = 'RESTOCKINGFEE'
  AND to_summary_field_when_positive_amount = 'total_refund_expense_or_sales_amt'
  AND to_summary_field_when_negative_amount = 'total_refund_expense_or_sales_amt';

-- Defect:          [pass B, O5] Buyer recharge and other adjustments routed to a field with no Summary line
-- Kind:            mapping defect
-- Rule:            P-020, P-038, P-150 (payments, other); S-028, S-030 (settlement OTHER-TRANSACTION); S-138 (PROMOTION_ADJUSTMENT)
-- Old value:       all six rules, both signs: total_adjustment_other_buyer_recharge_amt
-- New value:       P-020, P-038, P-150, S-028, S-030: positive sales_other, negative expenses_other;
--                  S-138: both signs expenses_promotional_rebates
-- Why:             total_adjustment_other_buyer_recharge_amt has no Summary line, so these adjustments reach none. The
--                  house pattern for other adjustments is P-108 and S-041: a positive adjustment is income (Sales › Other)
--                  and a negative one is a cost (Expenses › Other). A promotion adjustment is a promotion, so it goes with
--                  the ORDER promotions in Promo rebates (S-072, S-073). S-138 has no payments counterpart, so it would show
--                  as a Variance if it fired; it has no in-scope line here.
-- Variance closed: none. Sales › Other 11.61 → 11.61, Expenses › Other 0.00 → 0.00 and Expenses › Promo rebates -11,273.53 → -11,273.53 (no lines).
UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_other',
    to_summary_field_when_negative_amount = 'expenses_other'
WHERE rule_id IN ('P-020', 'P-038', 'P-150') AND amount_field = 'other'
  AND to_summary_field_when_positive_amount = 'total_adjustment_other_buyer_recharge_amt'
  AND to_summary_field_when_negative_amount = 'total_adjustment_other_buyer_recharge_amt';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_other',
    to_summary_field_when_negative_amount = 'expenses_other'
WHERE rule_id IN ('S-028', 'S-030') AND transaction_type = 'OTHER-TRANSACTION'
  AND to_summary_field_when_positive_amount = 'total_adjustment_other_buyer_recharge_amt'
  AND to_summary_field_when_negative_amount = 'total_adjustment_other_buyer_recharge_amt';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'expenses_promotional_rebates',
    to_summary_field_when_negative_amount = 'expenses_promotional_rebates'
WHERE rule_id = 'S-138' AND amount_description = 'PROMOTION_ADJUSTMENT'
  AND to_summary_field_when_positive_amount = 'total_adjustment_other_buyer_recharge_amt'
  AND to_summary_field_when_negative_amount = 'total_adjustment_other_buyer_recharge_amt';

-- Defect:          [pass B, O6] Micro deposit round-off routed to a field with no Summary line; its Summary line has no field
-- Kind:            mapping defect
-- Rule:            P-061 (TRANSFER MICRO_DEPOSIT, other); S-060 (TRANSFERS MICRO_DEPOSIT); Summary line Expenses › Micro Deposit
-- Old value:       P-061 and S-060, both signs: bank_account_transfer_round_off;
--                  Summary line Expenses › Micro Deposit: summary_field is empty
-- New value:       P-061 and S-060, both signs: expenses_micro_deposit;
--                  Summary line Expenses › Micro Deposit: summary_field = 'expenses_micro_deposit'
-- Why:             bank_account_transfer_round_off has no Summary line. The Summary already has an Expenses › Micro Deposit
--                  line (display order 200) with no field, and that is the line a micro-deposit amount belongs on. The first
--                  statement sets the field on that existing row of summary_line_map. The row is in the database, so the
--                  change is in mapping_rule_history, and configs/summary_line_map.csv is unchanged. Both rules route both
--                  signs to the one field, so the two sides meet on the same line. Neither rule has an in-scope line here.
-- Variance closed: none. Expenses › Micro Deposit 0.00 → 0.00 (no lines). Nothing routes to bank_account_transfer_round_off any more.
UPDATE summary_line_map
SET summary_field = 'expenses_micro_deposit'
WHERE summary_group = 'Expenses' AND summary_line = 'Micro Deposit' AND summary_field IS NULL;

UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'expenses_micro_deposit',
    to_summary_field_when_negative_amount = 'expenses_micro_deposit'
WHERE rule_id = 'P-061' AND transaction_type = 'TRANSFER' AND amount_field = 'other'
  AND to_summary_field_when_positive_amount = 'bank_account_transfer_round_off'
  AND to_summary_field_when_negative_amount = 'bank_account_transfer_round_off';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'expenses_micro_deposit',
    to_summary_field_when_negative_amount = 'expenses_micro_deposit'
WHERE rule_id = 'S-060' AND transaction_type = 'TRANSFERS' AND amount_type = 'MICRO_DEPOSIT'
  AND to_summary_field_when_positive_amount = 'bank_account_transfer_round_off'
  AND to_summary_field_when_negative_amount = 'bank_account_transfer_round_off';

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

-- ===== Accounting-policy calls, pass B (approved and not approved by the reviewer, 2026-10-28) =====

-- Defect:          FBA per-unit fulfilment fee routed to Amazon fees, while the Summary has an FBA fees line
-- Kind:            accounting-policy call (applied; reviewer decision 2026-10-28)
-- Recommendation:  Route the ORDER FBA fee to Expenses › FBA fees on both sides, so the two sides agree. The payments
--                  column is labelled "fulfilment by amazon fees", and the Summary has an FBA fees line for it. The same
--                  column also carries the shipping and gift wrap chargebacks (-1,210.91), which the column cannot split
--                  (the schema limitation above), so they move with it. The alternative, Sales › FBA Fees, has no field and
--                  was not chosen.
-- Rule:            payments P-073; settlement S-067, S-068 and S-069
-- Old value:       P-073, both signs: expenses_amazon_fees; S-067, S-068 and S-069, both signs: expenses_amazon_fees
-- New value:       P-073, both signs: expenses_fba_fees; S-067, S-068 and S-069, both signs: expenses_fba_fees
-- Why:             Both sides carry the same money: payments -96,803.17 on P-073, and settlement -95,592.26, -11.97 and
--                  -1,198.94 on the three rules, which sum to -96,803.17. The settlement labels the first of them
--                  FBAPERUNITFULFILLMENTFEE, so the cost is an FBA cost. The sample workbook has headers only, so it does
--                  not settle the label. Moving the four rules together keeps the two sides on the same line.
-- Variance closed: Expenses › Amazon fees 0.00 → 0.00 (payments and settlements each -132,593.41 → -35,790.24);
--                  Expenses › FBA fees 0.00 → 0.00 (payments and settlements each -0.41 → -96,803.58). The moved amount is
--                  -96,803.17 on each side.
-- Applied:         yes (reviewer decision 2026-10-28)
UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'expenses_fba_fees',
    to_summary_field_when_negative_amount = 'expenses_fba_fees'
WHERE rule_id = 'P-073' AND transaction_type = 'ORDER' AND amount_field = 'fba_fees'
  AND to_summary_field_when_positive_amount = 'expenses_amazon_fees'
  AND to_summary_field_when_negative_amount = 'expenses_amazon_fees';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'expenses_fba_fees',
    to_summary_field_when_negative_amount = 'expenses_fba_fees'
WHERE rule_id IN ('S-067', 'S-068', 'S-069') AND transaction_type = 'ORDER' AND amount_type = 'ITEMFEES'
  AND to_summary_field_when_positive_amount = 'expenses_amazon_fees'
  AND to_summary_field_when_negative_amount = 'expenses_amazon_fees';

-- Defect:          Sales tax on retro-charge, chargeback, liquidation and A-to-Z lines routed to Other, Refund expenses or Amazon fees
-- Kind:            accounting-policy call (applied; reviewer decision 2026-10-28)
-- Recommendation:  Extend the sales-tax policy to the remaining tax-shaped rules, so that every sales tax and every refund
--                  tax routes to Sales › Tax. Income tax (S-042) and tax on Amazon fees (S-045, S-100, S-117, S-129, S-131,
--                  S-133, S-135, S-137) are not sales tax and stay where they are, with the fee.
-- Rule:            payments P-003 and P-130; settlement S-009, S-078, S-085, S-086, S-087, S-088, S-089, S-094, S-096,
--                  S-097, S-098, S-080, S-081, S-103, S-104, S-105, S-107, S-109, S-120 and S-123 (22 rules in all)
-- Old value:       P-003 both signs expenses_other; P-130 both signs sales_other;
--                  S-009, S-078, S-085, S-097 and S-098 both signs sales_other;
--                  S-086, S-087, S-088, S-089, S-094, S-096, S-103, S-104, S-105, S-107, S-109 and S-123 both signs expenses_other;
--                  S-080, S-081 and S-120 both signs refunded_expenses
-- New value:       all 22 rules, both signs: sales_tax
-- Why:             These rules carry sales tax or refund tax on retro charges, chargebacks, liquidations and A-to-Z
--                  claims. The same policy routes that tax to Sales › Tax on ordinary lines, so these rules would
--                  leave it in Other, Refund expenses and Sales › Other. Within the retro-charge family the two sides
--                  already disagree: P-130 sends ORDER_RETROCHARGE sales tax to Sales › Other, and the settlement splits
--                  the same retro-charge tax between Sales › Other (S-009, S-085) and Expenses › Other (S-086 to S-089).
--                  None of the 22 rules has an in-scope line in this settlement.
-- Variance closed: none. Sales › Tax 13,436.38 → 13,436.38, and Sales › Other 11.61, Expenses › Other 0.00 and
--                  Refunds › Refund expenses 258.87 are unchanged (no line fires any of the 22 rules).
-- Applied:         yes (reviewer decision 2026-10-28)
UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'P-003' AND to_summary_field_when_positive_amount = 'expenses_other'
  AND to_summary_field_when_negative_amount = 'expenses_other';

UPDATE payment_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id = 'P-130' AND to_summary_field_when_positive_amount = 'sales_other'
  AND to_summary_field_when_negative_amount = 'sales_other';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id IN ('S-009', 'S-078', 'S-085', 'S-097', 'S-098')
  AND to_summary_field_when_positive_amount = 'sales_other'
  AND to_summary_field_when_negative_amount = 'sales_other';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id IN ('S-086', 'S-087', 'S-088', 'S-089', 'S-094', 'S-096', 'S-103', 'S-104', 'S-105', 'S-107', 'S-109', 'S-123')
  AND to_summary_field_when_positive_amount = 'expenses_other'
  AND to_summary_field_when_negative_amount = 'expenses_other';

UPDATE settlement_mapping_rules
SET to_summary_field_when_positive_amount = 'sales_tax',
    to_summary_field_when_negative_amount = 'sales_tax'
WHERE rule_id IN ('S-080', 'S-081', 'S-120')
  AND to_summary_field_when_positive_amount = 'refunded_expenses'
  AND to_summary_field_when_negative_amount = 'refunded_expenses';

-- Defect:          Chargeback and A-to-Z principal and shipping reversals routed to Expenses › Other, while the same
--                  component on a refund goes to Refunds › Refunded sales
-- Kind:            accounting-policy call (not applied; reviewer decision 2026-10-28)
-- Recommendation:  Route these reversals to Refunds › Refunded sales, as the refund product sales and shipping are routed
--                  (P-009, P-010, S-010, S-013). The fees on chargebacks and A-to-Z claims (P-076, P-113, P-115, S-083,
--                  S-084, S-110 to S-112) stay in Sales › Amazon fees.
-- Rule:            payments P-075, P-116 and P-117; settlement S-082, S-106 and S-108
-- Old value:       all six rules, both signs: expenses_other
-- New value:       all six rules, both signs: refunded_sales
-- Why:             Both sides agree (Expenses › Other), so there is no Variance. The choice is one of presentation.
--                  Defensible either way; a reversal of a sale reads better in Refunds.
-- Variance closed: none. Refunds › Refunded sales -2,031.37 → -2,031.37 and Expenses › Other 0.00 → 0.00 (no line fires).
-- Applied:         no (reviewer decision 2026-10-28)
-- UPDATE payment_mapping_rules
-- SET to_summary_field_when_positive_amount = 'refunded_sales',
--     to_summary_field_when_negative_amount = 'refunded_sales'
-- WHERE rule_id IN ('P-116', 'P-117', 'P-075') AND to_summary_field_when_positive_amount = 'expenses_other'
--   AND to_summary_field_when_negative_amount = 'expenses_other';
-- UPDATE settlement_mapping_rules
-- SET to_summary_field_when_positive_amount = 'refunded_sales',
--     to_summary_field_when_negative_amount = 'refunded_sales'
-- WHERE rule_id IN ('S-106', 'S-108', 'S-082') AND to_summary_field_when_positive_amount = 'expenses_other'
--   AND to_summary_field_when_negative_amount = 'expenses_other';

-- Defect:          FAILED_DISBURSEMENT and failed bank transfer route to nothing; Sales › Micro Deposit (Failed) has no field
-- Kind:            accounting-policy call (not applied; reviewer decision 2026-10-28)
-- Recommendation:  Give the line the field sales_micro_deposit_failed (a summary_line_map row) and route P-135 and S-124
--                  there. Not applied: S-124 is a bank transfer that failed, which may be a balance movement rather than
--                  a P&L item, and the reviewer did not take that call. Until it is decided the line stays empty.
-- Rule:            payments P-135 (ADJUSTMENT FAILED_DISBURSEMENT, total column); settlement S-124 (OTHER-TRANSACTION)
-- Old value:       P-135 and S-124 blank on both signs; Summary line Sales › Micro Deposit (Failed) has no summary_field
-- New value:       P-135 and S-124, both signs: sales_micro_deposit_failed; Sales › Micro Deposit (Failed) = 'sales_micro_deposit_failed'
-- Why:             Both configs carry the failure and route nothing, so the line is always zero. The two sides agree at zero,
--                  so there is no Variance to close. The routing choice only matters if a failed disbursement occurs.
-- Variance closed: none. Sales › Micro Deposit (Failed) 0.00 → 0.00 (no lines).
-- Applied:         no (reviewer decision 2026-10-28)
-- UPDATE summary_line_map
-- SET summary_field = 'sales_micro_deposit_failed'
-- WHERE summary_group = 'Sales' AND summary_line = 'Micro Deposit (Failed)' AND summary_field IS NULL;
-- UPDATE payment_mapping_rules
-- SET to_summary_field_when_positive_amount = 'sales_micro_deposit_failed',
--     to_summary_field_when_negative_amount = 'sales_micro_deposit_failed'
-- WHERE rule_id = 'P-135' AND to_summary_field_when_positive_amount = '' AND to_summary_field_when_negative_amount = '';
-- UPDATE settlement_mapping_rules
-- SET to_summary_field_when_positive_amount = 'sales_micro_deposit_failed',
--     to_summary_field_when_negative_amount = 'sales_micro_deposit_failed'
-- WHERE rule_id = 'S-124' AND to_summary_field_when_positive_amount = '' AND to_summary_field_when_negative_amount = '';

-- Defect:          Return postage routed to Expenses › Other, while Expenses › Shipping Charges has no field
-- Kind:            accounting-policy call (not applied; reviewer decision 2026-10-28)
-- Recommendation:  Route return postage and return label purchases to Expenses › Shipping Charges (a summary_line_map row).
--                  Shipping chargebacks (S-069) stay in Expenses › Amazon fees, as P1 keeps them with the FBA fee. The
--                  reviewer did not take this call, so the postage stays in Expenses › Other.
-- Rule:            payments P-107 (SHIPPING_SERVICES RETURNPOSTAGEBILLING, other); settlement S-026 (OTHER-TRANSACTION)
-- Old value:       P-107 and S-026, both signs: expenses_other; Summary line Expenses › Shipping Charges has no summary_field
-- New value:       P-107 and S-026, both signs: expenses_shipping_charges; Expenses › Shipping Charges = 'expenses_shipping_charges'
-- Why:             Return postage is a shipping cost, so Shipping Charges is the line it belongs on. Defensible either way.
--                  Both sides route it to the same line today, so there is no Variance.
-- Variance closed: none. Expenses › Other 0.00 → 0.00 and Expenses › Shipping Charges 0.00 → 0.00 (no lines).
-- Applied:         no (reviewer decision 2026-10-28)
-- UPDATE summary_line_map
-- SET summary_field = 'expenses_shipping_charges'
-- WHERE summary_group = 'Expenses' AND summary_line = 'Shipping Charges' AND summary_field IS NULL;
-- UPDATE payment_mapping_rules
-- SET to_summary_field_when_positive_amount = 'expenses_shipping_charges',
--     to_summary_field_when_negative_amount = 'expenses_shipping_charges'
-- WHERE rule_id = 'P-107' AND to_summary_field_when_positive_amount = 'expenses_other'
--   AND to_summary_field_when_negative_amount = 'expenses_other';
-- UPDATE settlement_mapping_rules
-- SET to_summary_field_when_positive_amount = 'expenses_shipping_charges',
--     to_summary_field_when_negative_amount = 'expenses_shipping_charges'
-- WHERE rule_id = 'S-026' AND to_summary_field_when_positive_amount = 'expenses_other'
--   AND to_summary_field_when_negative_amount = 'expenses_other';
