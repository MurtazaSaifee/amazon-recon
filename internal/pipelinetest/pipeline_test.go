// Package pipelinetest holds the seam-1 tests: synthetic report files go in,
// the reconciliation report and the workbook come out. They need PostgreSQL
// and skip with a message when RECON_DATABASE_URL is not set.
package pipelinetest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/xuri/excelize/v2"

	"amazonrecon/internal/ingest"
	"amazonrecon/internal/recon"
	"amazonrecon/internal/report"
	"amazonrecon/internal/store"
)

const fixtures = "../../testdata/fixtures"

// Line numbers in the synthetic fixtures (see testdata/fixtures).
const (
	pmtReleasedBeforeMidnightUTC = 11 // release 3 Jul 08:30 GMT+9 = 2 Jul 23:30 UTC
	pmtReleasedAfterMidnightUTC  = 12 // release 3 Jul 10:30 GMT+9 = 3 Jul 01:30 UTC
	pmtRefundExactlyMidnightUTC  = 13 // release 4 Jul 09:00 GMT+9 = 4 Jul 00:00 UTC
	pmtTransfer                  = 15
	pmtDeferred                  = 16 // no release date, falls back to date/time
	pmtOtherSettlementReleased   = 18
	setHeader                    = 2
)

const (
	settlementID = "12000000001"
	tieOutSheet  = "Tie-out"
)

func TestPipelineTracerBullet(t *testing.T) {
	ctx := context.Background()
	pool := freshDatabase(t)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	res := ingestFixtures(t, pool)
	rep, err := recon.Build(ctx, pool, res.RunID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	t.Run("settlement total comes from the header line", func(t *testing.T) {
		if rep.Settlement.ID != settlementID {
			t.Errorf("settlement ID = %q, want %q", rep.Settlement.ID, settlementID)
		}
		assertDec(t, "settlement total", rep.Settlement.Total, "1249.56")
		assertLine(t, ctx, pool, res.RunID, "settlement", setHeader, func(l store.Line) {
			if l.LineKind != "settlement_header" || l.InScope {
				t.Errorf("header line kind/scope = %q/%v, want settlement_header/false", l.LineKind, l.InScope)
			}
		})
	})

	t.Run("in-scope components tie to the header total", func(t *testing.T) {
		if rep.Components.Count != 4 {
			t.Errorf("in-scope components = %d, want 4", rep.Components.Count)
		}
		assertDec(t, "in-scope components", rep.Components.Amount, "1249.56")
		assertDec(t, "components minus settlement total", rep.ComponentsMinusTotal, "0.00")
	})

	t.Run("released payments excluding the transfer tie to the total", func(t *testing.T) {
		if rep.ReleasedExcludingTransfer.Count != 4 {
			t.Errorf("released lines excluding transfer = %d, want 4", rep.ReleasedExcludingTransfer.Count)
		}
		assertDec(t, "released excluding transfer", rep.ReleasedExcludingTransfer.Amount, "1249.56")
		assertDec(t, "released excluding transfer minus settlement total", rep.ExcludingTransferMinusTotal, "0.00")
	})

	t.Run("transfer is in scope and shown on its own", func(t *testing.T) {
		assertDec(t, "transfer", rep.Transfers.Amount, "-1000.00")
		assertDec(t, "in-scope transaction lines incl. transfer", rep.TransactionsInScope.Amount, "249.56")
		assertLine(t, ctx, pool, res.RunID, "payments", pmtTransfer, func(l store.Line) {
			if !l.InScope {
				t.Errorf("transfer line out of scope: %s", l.ScopeReason)
			}
		})
	})

	t.Run("deferred lines are a memo and other settlements are out of scope", func(t *testing.T) {
		if rep.Deferred.Count != 2 {
			t.Errorf("deferred memo count = %d, want 2", rep.Deferred.Count)
		}
		assertDec(t, "deferred memo amount", rep.Deferred.Amount, "125.50")
		assertLine(t, ctx, pool, res.RunID, "payments", pmtDeferred, func(l store.Line) {
			if l.InScope || l.ScopeReason != "Deferred" {
				t.Errorf("deferred line scope = %v/%q, want out/Deferred", l.InScope, l.ScopeReason)
			}
		})
		assertLine(t, ctx, pool, res.RunID, "payments", pmtOtherSettlementReleased, func(l store.Line) {
			if l.InScope || l.ScopeReason != "Other settlement" {
				t.Errorf("other settlement line scope = %v/%q, want out/Other settlement", l.InScope, l.ScopeReason)
			}
		})
	})

	t.Run("posted dates are UTC dates on either side of midnight", func(t *testing.T) {
		assertPostedDate(t, ctx, pool, res.RunID, "payments", pmtReleasedBeforeMidnightUTC, "2026-07-02", "release_date_utc")
		assertPostedDate(t, ctx, pool, res.RunID, "payments", pmtReleasedAfterMidnightUTC, "2026-07-03", "release_date_utc")
		assertPostedDate(t, ctx, pool, res.RunID, "payments", pmtRefundExactlyMidnightUTC, "2026-07-04", "release_date_utc")
		assertPostedDate(t, ctx, pool, res.RunID, "payments", pmtDeferred, "2026-07-09", "date_time_utc")
		assertPostedDate(t, ctx, pool, res.RunID, "settlement", 3, "2026-07-02", "posted_date")
	})

	t.Run("file names, hashes and line counts are recorded", func(t *testing.T) {
		wantPayments := fileSHA256(t, filepath.Join(fixtures, "synthetic_payments.csv"))
		wantSettlement := fileSHA256(t, filepath.Join(fixtures, "synthetic_settlement.txt"))
		got := map[string]recon.FileRecord{}
		for _, f := range rep.Files {
			got[f.Source] = f
		}
		p, s := got["payments"], got["settlement"]
		if p.FileName != "synthetic_payments.csv" || p.SHA256 != wantPayments {
			t.Errorf("payments file record = %q %s, want synthetic_payments.csv %s", p.FileName, p.SHA256, wantPayments)
		}
		if s.FileName != "synthetic_settlement.txt" || s.SHA256 != wantSettlement {
			t.Errorf("settlement file record = %q %s, want synthetic_settlement.txt %s", s.FileName, s.SHA256, wantSettlement)
		}
		if p.PhysicalLines != 19 || p.StoredLines != 9 || p.MalformedLines != 0 {
			t.Errorf("payments counts = physical %d stored %d malformed %d, want 19/9/0", p.PhysicalLines, p.StoredLines, p.MalformedLines)
		}
		if s.PhysicalLines != 6 || s.StoredLines != 5 || s.MalformedLines != 0 {
			t.Errorf("settlement counts = physical %d stored %d malformed %d, want 6/5/0", s.PhysicalLines, s.StoredLines, s.MalformedLines)
		}
		if rep.StartedAt.IsZero() || rep.FinishedAt.IsZero() {
			t.Errorf("run timestamps not recorded: started %v finished %v", rep.StartedAt, rep.FinishedAt)
		}
	})

	t.Run("workbook shows the tie-out", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "tieout.xlsx")
		if err := report.WriteWorkbook(path, rep); err != nil {
			t.Fatalf("write workbook: %v", err)
		}
		f, err := excelize.OpenFile(path)
		if err != nil {
			t.Fatalf("open workbook: %v", err)
		}
		defer f.Close()
		checkCell(t, f, "Settlement total (Settlement report header)", "C", "1249.56")
		checkCell(t, f, "In-scope Components", "B", "4")
		checkCell(t, f, "In-scope Components", "C", "1249.56")
		checkCell(t, f, "Components minus Settlement total", "C", "0.00")
		checkCell(t, f, "In-scope Transaction lines (Released, incl. Transfer)", "C", "249.56")
		checkCell(t, f, "Transfer (in scope)", "C", "-1000.00")
		checkCell(t, f, "In-scope Released excl. Transfer", "C", "1249.56")
		checkCell(t, f, "Released excl. Transfer minus Settlement total", "C", "0.00")
		checkCell(t, f, "Deferred memo (out of scope)", "B", "2")
		checkCell(t, f, "Deferred memo (out of scope)", "C", "125.50")
	})

	t.Run("re-ingesting leaves counts and totals unchanged", func(t *testing.T) {
		again := ingestFixtures(t, pool)
		rep2, err := recon.Build(ctx, pool, again.RunID)
		if err != nil {
			t.Fatalf("reconcile second run: %v", err)
		}
		for name, pair := range map[string][2]recon.Total{
			"components":    {rep.Components, rep2.Components},
			"transactions":  {rep.TransactionsInScope, rep2.TransactionsInScope},
			"deferred memo": {rep.Deferred, rep2.Deferred},
		} {
			if pair[0].Count != pair[1].Count || !pair[0].Amount.Equal(pair[1].Amount) {
				t.Errorf("%s changed after re-ingest: %+v -> %+v", name, pair[0], pair[1])
			}
		}
		if !rep2.Settlement.Total.Equal(rep.Settlement.Total) {
			t.Errorf("settlement total changed after re-ingest: %s -> %s", rep.Settlement.Total, rep2.Settlement.Total)
		}
		if len(rep2.Files) != len(rep.Files) {
			t.Fatalf("file records = %d, want %d", len(rep2.Files), len(rep.Files))
		}
		for i := range rep.Files {
			if rep2.Files[i].StoredLines != rep.Files[i].StoredLines {
				t.Errorf("%s stored lines changed: %d -> %d", rep.Files[i].Source, rep.Files[i].StoredLines, rep2.Files[i].StoredLines)
			}
		}
	})
}

// A malformed line fails the run and replaces no stored lines, so the
// previous complete run still stands.
func TestMalformedLineFailsRunAndReplacesNothing(t *testing.T) {
	ctx := context.Background()
	pool := freshDatabase(t)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	good := ingestFixtures(t, pool)
	before, err := recon.Build(ctx, pool, good.RunID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	original, err := os.ReadFile(filepath.Join(fixtures, "synthetic_payments.csv"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	broken := append(original, []byte("\"only one column\"\n")...)
	badPath := filepath.Join(t.TempDir(), "synthetic_payments.csv")
	if err := os.WriteFile(badPath, broken, 0o644); err != nil {
		t.Fatalf("write broken copy: %v", err)
	}

	_, err = ingest.Run(ctx, pool, ingest.Inputs{
		PaymentsPath:   badPath,
		SettlementPath: filepath.Join(fixtures, "synthetic_settlement.txt"),
	})
	if err == nil {
		t.Fatal("ingest with a malformed line succeeded, want it to fail")
	}

	latest, err := store.LatestCompleteRun(ctx, pool)
	if err != nil {
		t.Fatalf("latest complete run: %v", err)
	}
	if latest != good.RunID {
		t.Errorf("latest complete run = %d, want the earlier run %d", latest, good.RunID)
	}
	after, err := recon.Build(ctx, pool, good.RunID)
	if err != nil {
		t.Fatalf("reconcile earlier run: %v", err)
	}
	if after.Deferred.Count != before.Deferred.Count || !after.Components.Amount.Equal(before.Components.Amount) ||
		after.Components.Count != before.Components.Count {
		t.Errorf("earlier run changed after a failed run: before %+v after %+v", before, after)
	}
}

// freshDatabase creates an empty database for one test and drops it on cleanup.
func freshDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("RECON_DATABASE_URL")
	if base == "" {
		t.Skip("RECON_DATABASE_URL is not set: skipping the seam-1 pipeline test (it needs PostgreSQL, see docker-compose.yml)")
	}
	ctx := context.Background()
	name := "recon_test_" + randomHex(t, 6)

	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		admin.Close()
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)")
		admin.Close()
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse RECON_DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatalf("connect to %s: %v", name, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func ingestFixtures(t *testing.T, pool *pgxpool.Pool) ingest.Result {
	t.Helper()
	res, err := ingest.Run(context.Background(), pool, ingest.Inputs{
		PaymentsPath:   filepath.Join(fixtures, "synthetic_payments.csv"),
		SettlementPath: filepath.Join(fixtures, "synthetic_settlement.txt"),
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return res
}

func assertDec(t *testing.T, what string, got decimal.Decimal, want string) {
	t.Helper()
	w := decimal.RequireFromString(want)
	if !got.Equal(w) {
		t.Errorf("%s = %s, want %s", what, got.StringFixed(2), want)
	}
}

func assertLine(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID int64, source string, lineNumber int, check func(store.Line)) {
	t.Helper()
	l, err := store.LoadLine(ctx, pool, runID, source, lineNumber)
	if err != nil {
		t.Fatalf("load %s line %d: %v", source, lineNumber, err)
	}
	check(l)
}

func assertPostedDate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, runID int64, source string, lineNumber int, wantDate, wantBasis string) {
	t.Helper()
	assertLine(t, ctx, pool, runID, source, lineNumber, func(l store.Line) {
		if got := l.PostedDate.Format("2006-01-02"); got != wantDate {
			t.Errorf("%s line %d posted date = %s, want %s", source, lineNumber, got, wantDate)
		}
		if l.PostedDateBasis != wantBasis {
			t.Errorf("%s line %d date basis = %q, want %q", source, lineNumber, l.PostedDateBasis, wantBasis)
		}
	})
}

// checkCell finds the row whose column A equals label and compares the raw value of column col.
func checkCell(t *testing.T, f *excelize.File, label, col, want string) {
	t.Helper()
	rows, err := f.GetRows(tieOutSheet, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}
	colIdx := int(col[0] - 'A')
	for _, r := range rows {
		if len(r) > 0 && r[0] == label {
			got := ""
			if colIdx < len(r) {
				got = r[colIdx]
			}
			if got != want {
				t.Errorf("workbook %q column %s = %q, want %q", label, col, got, want)
			}
			return
		}
	}
	t.Errorf("workbook has no row labelled %q", label)
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random: %v", err)
	}
	return hex.EncodeToString(b)
}
