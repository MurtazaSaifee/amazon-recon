package pipelinetest

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"amazonrecon/internal/explain"
)

// Expected explanations are worked by hand from the synthetic fixtures (see variance_test.go).
// Sales / Tax: payments 469.12 (P-003 and P-004 each route 234.56 from line 11), settlements 234.56
// (S-003 from line 4), so the Variance is 234.56. The only record with a difference is line 11's.

func TestExplainTracesVarianceToRulesRecordsAndLines(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	res := ingestFixtures(t, pool)

	t.Run("a line name parses as group and line", func(t *testing.T) {
		got, err := explain.ParseLine("Sales/Tax")
		if err != nil || got.Group != "Sales" || got.Name != "Tax" {
			t.Fatalf("ParseLine(Sales/Tax) = %+v, %v; want Sales and Tax", got, err)
		}
		if _, err := explain.ParseLine("Tax"); err == nil {
			t.Error("ParseLine without a group was accepted, want an error")
		}
	})

	t.Run("an unknown Summary line is an error, not an empty explanation", func(t *testing.T) {
		if _, err := explain.Load(ctx, pool, res.RunID, explain.Line{Group: "Sales", Name: "Nope"}); err == nil {
			t.Error("Load of an unknown Summary line returned no error")
		}
	})

	t.Run("the variance and the contributions by rule on each side", func(t *testing.T) {
		e := loadExplanation(t, ctx, pool, res.RunID, "Sales", "Tax")
		assertDec(t, "payments", e.Payments, "469.12")
		assertDec(t, "settlements", e.Settlements, "234.56")
		assertDec(t, "variance", e.Variance, "234.56")
		if e.Field != "sales_tax" {
			t.Errorf("summary field = %q, want sales_tax", e.Field)
		}
		assertRules(t, e.PaymentRules, []string{"P-003", "P-004"}, "234.56", 1)
		assertRules(t, e.SettlementRules, []string{"S-003"}, "234.56", 1)
	})

	t.Run("the top record_refs by absolute difference, with their source line numbers", func(t *testing.T) {
		e := loadExplanation(t, ctx, pool, res.RunID, "Sales", "Tax")
		if len(e.Records) != 1 {
			t.Fatalf("records = %d, want 1", len(e.Records))
		}
		r := e.Records[0]
		if r.RecordRef != "701-0000001-0000001|SYN-A-001|2026-07-02" {
			t.Errorf("record_ref = %q", r.RecordRef)
		}
		assertDec(t, "record difference", r.Difference, "234.56")
		assertLineNumbers(t, "payments lines", r.PaymentsLines, "synthetic_payments.csv", 11)
		assertLineNumbers(t, "settlement lines", r.SettlementLines, "synthetic_settlement.txt", 4)
	})

	t.Run("a line that agrees on both sides has a zero variance and no records to list", func(t *testing.T) {
		e := loadExplanation(t, ctx, pool, res.RunID, "Sales", "Product Charges")
		assertDec(t, "variance", e.Variance, "0.00")
		if len(e.Records) != 0 {
			t.Errorf("records = %d, want none: every record agrees", len(e.Records))
		}
	})

	t.Run("a payments-only line names the payments line that caused it", func(t *testing.T) {
		e := loadExplanation(t, ctx, pool, res.RunID, "Refunds", "Refunded sales")
		assertDec(t, "variance", e.Variance, "-10.00")
		if len(e.Records) != 1 {
			t.Fatalf("records = %d, want 1", len(e.Records))
		}
		assertLineNumbers(t, "payments lines", e.Records[0].PaymentsLines, "synthetic_payments.csv", 13)
		if len(e.Records[0].SettlementLines) != 0 {
			t.Errorf("settlement lines = %v, want none", e.Records[0].SettlementLines)
		}
	})

	t.Run("the formatted text names the rule, the record and its line", func(t *testing.T) {
		e := loadExplanation(t, ctx, pool, res.RunID, "Sales", "Tax")
		text := explain.Format(e)
		for _, want := range []string{"Variance", "234.56", "P-003", "P-004", "S-003",
			"701-0000001-0000001|SYN-A-001|2026-07-02", "synthetic_payments.csv", "11"} {
			if !strings.Contains(text, want) {
				t.Errorf("formatted explanation lacks %q:\n%s", want, text)
			}
		}
	})
}

func loadExplanation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID int64, group, name string) explain.Explanation {
	t.Helper()
	e, err := explain.Load(ctx, pool, runID, explain.Line{Group: group, Name: name})
	if err != nil {
		t.Fatalf("explain %s/%s: %v", group, name, err)
	}
	return e
}

// assertRules checks that rules are exactly the wanted IDs, each routing the same amount on the given line count.
func assertRules(t *testing.T, got []explain.RuleAmount, wantIDs []string, amount string, lines int) {
	t.Helper()
	if len(got) != len(wantIDs) {
		t.Fatalf("rules = %+v, want %v", got, wantIDs)
	}
	for i, id := range wantIDs {
		if got[i].RuleID != id {
			t.Errorf("rule %d = %s, want %s", i, got[i].RuleID, id)
		}
		if !got[i].Amount.Equal(decimal.RequireFromString(amount)) || got[i].Lines != lines {
			t.Errorf("rule %s = %s over %d lines, want %s over %d", id, got[i].Amount, got[i].Lines, amount, lines)
		}
	}
}

// assertLineNumbers checks one source line, from the named file, in a record's list of lines.
func assertLineNumbers(t *testing.T, what string, got []explain.SourceLine, file string, line int) {
	t.Helper()
	if len(got) != 1 || got[0].FileName != file || got[0].LineNumber != line {
		t.Errorf("%s = %+v, want %s line %d", what, got, file, line)
	}
}
