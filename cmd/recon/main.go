// Command recon ingests the Amazon Payments and Settlement reports into PostgreSQL
// and writes the reconciliation tie-out workbook.
//
//	recon migrate     [--reset [--force-reset]]
//	recon seed-config [--input-dir DIR] [--payments-config FILE] [--settlement-config FILE] [--summary-map FILE]
//	recon ingest      [--input-dir DIR] [--payments FILE] [--settlement FILE] [--baseline]
//	recon apply-fixes [--file PATH]
//	recon report      [--out PATH]
//	recon explain     --line "<group>/<line>"
//	recon counts
//
// Every command reads the database from --db or $RECON_DATABASE_URL.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"amazonrecon/internal/explain"
	"amazonrecon/internal/ingest"
	"amazonrecon/internal/recon"
	"amazonrecon/internal/report"
	"amazonrecon/internal/source"
	"amazonrecon/internal/store"
	"amazonrecon/internal/timefmt"
)

const usage = `usage:
  recon migrate     [--db URL] [--reset [--force-reset]]
  recon seed-config [--db URL] [--input-dir DIR] [--payments-config FILE] [--settlement-config FILE] [--summary-map FILE]
  recon ingest      [--db URL] [--input-dir DIR] [--payments FILE] [--settlement FILE] [--baseline]
  recon apply-fixes [--db URL] [--file PATH]
  recon report      [--db URL] [--out PATH]
  recon explain     [--db URL] --line "<group>/<line>"
  recon counts      [--db URL]
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
	case "seed-config":
		err = cmdSeedConfig(ctx, os.Args[2:])
	case "ingest":
		err = cmdIngest(ctx, os.Args[2:])
	case "apply-fixes":
		err = cmdApplyFixes(ctx, os.Args[2:])
	case "report":
		err = cmdReport(ctx, os.Args[2:])
	case "explain":
		err = cmdExplain(ctx, os.Args[2:])
	case "counts":
		err = cmdCounts(ctx, os.Args[2:])
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
	return fs.String("db", os.Getenv("RECON_DATABASE_URL"), "PostgreSQL URL (default $RECON_DATABASE_URL)")
}

func connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	if url == "" {
		return nil, fmt.Errorf("no database: set --db or RECON_DATABASE_URL")
	}
	return store.Connect(ctx, url)
}

// checkResetTarget refuses a reset unless the database name starts with "recon",
// or force is set. A reset drops the public schema, so a wrong URL must not reach it.
func checkResetTarget(url string, force bool) error {
	if force {
		return nil
	}
	u, err := neturl.Parse(url)
	if err != nil {
		return fmt.Errorf("refusing to reset: cannot parse database URL: %w", err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" {
		return errors.New("refusing to reset: the database URL names no database")
	}
	if !strings.HasPrefix(name, "recon") {
		return fmt.Errorf("refusing to reset database %q: its name must start with \"recon\" (pass --force-reset to override)", name)
	}
	return nil
}

func cmdMigrate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	db := dbFlag(fs)
	reset := fs.Bool("reset", false, "drop the public schema first, so the database starts empty")
	forceReset := fs.Bool("force-reset", false, "allow --reset on a database whose name does not start with \"recon\"")
	fs.Parse(args)

	if *reset {
		if err := checkResetTarget(*db, *forceReset); err != nil {
			return err
		}
	}
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

// cmdSeedConfig loads the two mapping configs and the Summary-line map into their tables.
// Each file is read as given, so a rule's ID is its CSV line number.
func cmdSeedConfig(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("seed-config", flag.ExitOnError)
	db := dbFlag(fs)
	inputDir := fs.String("input-dir", "Assignment_050726_files", "directory holding the two configs")
	payments := fs.String("payments-config", "amazon_payment_configs_au_old.csv", "payments mapping config file name")
	settlement := fs.String("settlement-config", "amazon_settlement_configs_au.csv", "settlement mapping config file name")
	summaryMap := fs.String("summary-map", filepath.Join("configs", "summary_line_map.csv"), "Summary-line map CSV")
	fs.Parse(args)

	pool, err := connect(ctx, *db)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := store.SeedConfig(ctx, pool, store.ConfigFiles{
		Payments:   filepath.Join(*inputDir, *payments),
		Settlement: filepath.Join(*inputDir, *settlement),
		SummaryMap: *summaryMap,
	}); err != nil {
		return err
	}
	fmt.Println("mapping configs and Summary-line map seeded")
	return nil
}

func cmdIngest(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	db := dbFlag(fs)
	inputDir := fs.String("input-dir", "Assignment_050726_files", "directory holding the two reports")
	payments := fs.String("payments", "amazon_payments_data.csv", "payments report file name")
	settlement := fs.String("settlement", "amazon_settlements_data.txt", "settlement report file name")
	baseline := fs.Bool("baseline", false, "run in baseline mode: every interpretation switch off")
	fs.Parse(args)

	pool, err := connect(ctx, *db)
	if err != nil {
		return err
	}
	defer pool.Close()

	mode := ingest.ModeFixed
	if *baseline {
		mode = ingest.ModeBaseline
	}
	res, err := ingest.Run(ctx, pool, ingest.Inputs{
		PaymentsPath:   filepath.Join(*inputDir, *payments),
		SettlementPath: filepath.Join(*inputDir, *settlement),
		Mode:           mode,
	})
	if err != nil {
		return fmt.Errorf("run %d: %w", res.RunID, err)
	}
	fmt.Printf("run %d complete: settlement %s\n", res.RunID, res.SettlementID)
	fmt.Printf("  payments lines stored:   %d\n", res.PaymentsLines)
	fmt.Printf("  settlement lines stored: %d (header included)\n", res.SettlementLines)
	fmt.Printf("  malformed lines:         %d (stored in ingest_errors)\n", res.Malformed)
	fmt.Printf("  mode %s, config hash %.12s\n", res.Mode, res.ConfigHash)
	return nil
}

// cmdApplyFixes runs a mapping-fix file against the database in one transaction.
func cmdApplyFixes(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("apply-fixes", flag.ExitOnError)
	db := dbFlag(fs)
	file := fs.String("file", "MAPPING_FIXES.sql", "mapping-fix SQL file")
	fs.Parse(args)

	sql, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	pool, err := connect(ctx, *db)
	if err != nil {
		return err
	}
	defer pool.Close()

	results, err := store.ApplyFixes(ctx, pool, string(sql))
	if err != nil {
		return err
	}
	for _, r := range results {
		fmt.Printf("  statement %d: %d row(s) changed: %s\n", r.Number, r.RowsAffected, r.Head)
	}
	fmt.Printf("applied %s in one transaction: %d statement(s)\n", *file, len(results))
	return nil
}

// cmdExplain traces one Summary line's Variance in the latest complete run.
func cmdExplain(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("explain", flag.ExitOnError)
	db := dbFlag(fs)
	lineArg := fs.String("line", "", `Summary line as "<group>/<line>", for example "Expenses/Amazon fees"`)
	fs.Parse(args)
	if *lineArg == "" {
		return errors.New(`explain needs --line "<group>/<line>"`)
	}
	line, err := explain.ParseLine(*lineArg)
	if err != nil {
		return err
	}

	pool, err := connect(ctx, *db)
	if err != nil {
		return err
	}
	defer pool.Close()

	runID, err := store.LatestCompleteRun(ctx, pool)
	if err != nil {
		return err
	}
	e, err := explain.Load(ctx, pool, runID, line)
	if err != nil {
		return err
	}
	fmt.Print(explain.Format(e))
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

// cmdCounts prints the reconciliation counts per status for the latest complete run.
func cmdCounts(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("counts", flag.ExitOnError)
	db := dbFlag(fs)
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
	fmt.Printf("Run %d, Settlement %s\n", rep.RunID, rep.Settlement.ID)
	printCounts(rep.Reconciliation)
	return nil
}

func printCounts(r recon.Reconciliation) {
	fmt.Println("Reconciliation counts (record_refs):")
	for _, s := range recon.Statuses {
		fmt.Printf("  %-28s %6d\n", s.Label(), r.Counts[s])
	}
	fmt.Printf("  %-28s %6d lines (payments %d, settlements %d; no record_ref)\n", "Unmapped",
		r.UnmappedTotal(), r.UnmappedLines[source.Payments], r.UnmappedLines[source.Settlement])
}

func printTieOut(r recon.Report, out string) {
	fmt.Printf("Run %d, Settlement %s (period %s to %s UTC, deposit %s UTC)\n", r.RunID, r.Settlement.ID,
		timefmt.Date(r.Settlement.PeriodStart), timefmt.Date(r.Settlement.PeriodEnd),
		timefmt.Date(r.Settlement.DepositDate))
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
	printCounts(r.Reconciliation)
	amount("Settlement total (Settlement report header)", r.Settlement.Total)
	total("In-scope Components", r.Components)
	amount("  minus Settlement total (should be 0.00)", r.ComponentsMinusTotal)
	total("In-scope transaction lines (Released)", r.TransactionsInScope)
	total("  of which Unreconciled payments", r.TieOut.Payments.Unreconciled)
	amount("In-scope Released excluding Unreconciled payments", r.ReleasedExcludingUnreconciled)
	amount("  minus Settlement total (should be 0.00)", r.ReleasedExcludingUnreconciledMinusTotal)
	amount("Payments unexplained difference (should be 0.00)", r.TieOut.Payments.Unexplained)
	amount("Settlements unexplained difference (should be 0.00)", r.TieOut.Settlements.Unexplained)
	total("Deferred memo (out of scope)", r.Deferred)
	fmt.Printf("Workbook: %s\n", out)
}
