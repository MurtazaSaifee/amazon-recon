// Command recon ingests the Amazon Payments and Settlement reports into PostgreSQL
// and writes the reconciliation tie-out workbook.
//
//	recon migrate [--reset]
//	recon ingest  [--input-dir DIR] [--payments FILE] [--settlement FILE]
//	recon report  [--out PATH]
//
// Every command reads the database from --db or $DATABASE_URL.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"amazonrecon/internal/ingest"
	"amazonrecon/internal/recon"
	"amazonrecon/internal/report"
	"amazonrecon/internal/store"
)

const usage = `usage:
  recon migrate [--db URL] [--reset]
  recon ingest  [--db URL] [--input-dir DIR] [--payments FILE] [--settlement FILE]
  recon report  [--db URL] [--out PATH]
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx := context.Background()
	var err error
	switch os.Args[1] {
	case "migrate":
		err = cmdMigrate(ctx, os.Args[2:])
	case "ingest":
		err = cmdIngest(ctx, os.Args[2:])
	case "report":
		err = cmdReport(ctx, os.Args[2:])
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "recon:", err)
		os.Exit(1)
	}
}

func dbFlag(fs *flag.FlagSet) *string {
	return fs.String("db", os.Getenv("DATABASE_URL"), "PostgreSQL URL (default $DATABASE_URL)")
}

func connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	if url == "" {
		return nil, fmt.Errorf("no database: set --db or DATABASE_URL")
	}
	return store.Connect(ctx, url)
}

func cmdMigrate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	db := dbFlag(fs)
	reset := fs.Bool("reset", false, "drop the public schema first, so the database starts empty")
	fs.Parse(args)

	pool, err := connect(ctx, *db)
	if err != nil {
		return err
	}
	defer pool.Close()
	if *reset {
		if err := store.Reset(ctx, pool); err != nil {
			return fmt.Errorf("reset: %w", err)
		}
	}
	if err := store.Migrate(ctx, pool); err != nil {
		return err
	}
	fmt.Println("migrations are up to date")
	return nil
}

func cmdIngest(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	db := dbFlag(fs)
	inputDir := fs.String("input-dir", "Assignment_050726_files", "directory holding the two reports")
	payments := fs.String("payments", "amazon_payments_data.csv", "payments report file name")
	settlement := fs.String("settlement", "amazon_settlements_data.txt", "settlement report file name")
	fs.Parse(args)

	pool, err := connect(ctx, *db)
	if err != nil {
		return err
	}
	defer pool.Close()

	res, err := ingest.Run(ctx, pool, ingest.Inputs{
		PaymentsPath:   filepath.Join(*inputDir, *payments),
		SettlementPath: filepath.Join(*inputDir, *settlement),
	})
	if err != nil {
		return fmt.Errorf("run %d: %w", res.RunID, err)
	}
	fmt.Printf("run %d complete: settlement %s\n", res.RunID, res.SettlementID)
	fmt.Printf("  payments lines stored:   %d\n", res.PaymentsLines)
	fmt.Printf("  settlement lines stored: %d (header included)\n", res.SettlementLines)
	return nil
}

func cmdReport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	db := dbFlag(fs)
	out := fs.String("out", filepath.Join("reports", "recon_report.xlsx"), "workbook path")
	fs.Parse(args)

	pool, err := connect(ctx, *db)
	if err != nil {
		return err
	}
	defer pool.Close()

	runID, err := store.LatestCompleteRun(ctx, pool)
	if err != nil {
		return err
	}
	rep, err := recon.Build(ctx, pool, runID)
	if err != nil {
		return err
	}
	if err := report.WriteWorkbook(*out, rep); err != nil {
		return fmt.Errorf("write workbook: %w", err)
	}
	printTieOut(rep, *out)
	return nil
}

func printTieOut(r recon.Report, out string) {
	fmt.Printf("Run %d, Settlement %s (period %s to %s UTC, deposit %s UTC)\n", r.RunID, r.Settlement.ID,
		r.Settlement.PeriodStart.Format("2006-01-02"), r.Settlement.PeriodEnd.Format("2006-01-02"),
		r.Settlement.DepositDate.Format("2006-01-02"))
	amount := func(label string, d decimal.Decimal) {
		fmt.Printf("  %-56s %18s\n", label, report.FormatAmount(d))
	}
	total := func(label string, t recon.Total) {
		unit := "lines"
		if t.Count == 1 {
			unit = "line"
		}
		fmt.Printf("  %-56s %18s   %6d %s\n", label, report.FormatAmount(t.Amount), t.Count, unit)
	}
	amount("Settlement total (Settlement report header)", r.Settlement.Total)
	total("In-scope Components", r.Components)
	amount("  minus Settlement total (should be 0.00)", r.ComponentsMinusTotal)
	total("In-scope transaction lines (Released, incl. Transfer)", r.TransactionsInScope)
	total("  of which Transfer", r.Transfers)
	total("In-scope Released excluding Transfer", r.ReleasedExcludingTransfer)
	amount("  minus Settlement total (should be 0.00)", r.ExcludingTransferMinusTotal)
	total("Deferred memo (out of scope)", r.Deferred)
	fmt.Printf("Workbook: %s\n", out)
}
