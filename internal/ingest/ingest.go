// Package ingest orchestrates reading both reports, deciding scope from the
// Settlement report header, and storing the lines in one transaction.
package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"

	"amazonrecon/internal/mapping"
	"amazonrecon/internal/source"
	"amazonrecon/internal/store"
)

// Run modes, recorded on each run. Baseline turns the interpretation switches off.
const (
	ModeFixed    = "fixed"
	ModeBaseline = "baseline"
)

// Inputs names the two report files and the mode the run is recorded with.
type Inputs struct {
	PaymentsPath   string
	SettlementPath string
	Mode           string // ModeFixed or ModeBaseline; empty means ModeFixed
}

// Result is what a successful ingest run produced.
type Result struct {
	RunID           int64
	SettlementID    string
	PaymentsLines   int    // payments lines stored, in or out of scope
	SettlementLines int    // settlement lines stored, header included
	Malformed       int    // malformed lines of both reports, all stored in ingest_errors
	ConfigHash      string // SHA-256 of the mapping configs the run used
	Mode            string
}

// Run ingests both reports as one run. It replaces the stored lines of both
// reports in a single transaction. Malformed lines are stored in ingest_errors
// and ingestion continues. The run fails, and no stored lines are replaced, if
// any malformed line is in scope or its scope cannot be read.
func Run(ctx context.Context, pool *pgxpool.Pool, in Inputs) (Result, error) {
	if in.Mode == "" {
		in.Mode = ModeFixed
	}
	if in.Mode != ModeFixed && in.Mode != ModeBaseline {
		return Result{}, fmt.Errorf("unknown mode %q: want %q or %q", in.Mode, ModeFixed, ModeBaseline)
	}
	runID, err := store.BeginRun(ctx, pool)
	if err != nil {
		return Result{}, fmt.Errorf("begin run: %w", err)
	}
	res, err := run(ctx, pool, runID, in)
	if err != nil {
		if ferr := store.FinishRun(ctx, pool, runID, "failed", err.Error()); ferr != nil {
			return Result{RunID: runID}, fmt.Errorf("%w (and recording the failure: %v)", err, ferr)
		}
		return Result{RunID: runID}, err
	}
	if err := store.FinishRun(ctx, pool, runID, "complete", ""); err != nil {
		return res, fmt.Errorf("finish run: %w", err)
	}
	res.RunID = runID
	return res, nil
}

func run(ctx context.Context, pool *pgxpool.Pool, runID int64, in Inputs) (Result, error) {
	payDigest, err := digestFile(in.PaymentsPath)
	if err != nil {
		return Result{}, err
	}
	setDigest, err := digestFile(in.SettlementPath)
	if err != nil {
		return Result{}, err
	}

	settle, err := readSettlement(in.SettlementPath)
	if err != nil {
		return Result{}, err
	}
	pay, err := readPayments(in.PaymentsPath)
	if err != nil {
		return Result{}, err
	}

	headerID := settle.Header.SettlementID
	malformed := append(malformedLines(setDigest.name, settle.Errors, headerID),
		malformedLines(payDigest.name, pay.Errors, headerID)...)
	if len(malformed) > 0 {
		if err := store.RecordErrors(ctx, pool, runID, malformed); err != nil {
			return Result{}, fmt.Errorf("record malformed lines: %w", err)
		}
	}
	if n := blockingCount(malformed); n > 0 {
		first := firstInScope(malformed)
		return Result{Malformed: len(malformed)}, fmt.Errorf("%d malformed lines are in scope (first: %s line %d: %s); no lines were replaced, see ingest_errors",
			n, first.FileName, first.LineNumber, first.Reason)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx)

	// The rules are read inside the transaction, so every line is mapped with the configs the run stored.
	payRules, setRules, err := store.LoadRules(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	configHash, err := store.ConfigHash(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	switches := mapping.SwitchesFor(in.Mode == ModeBaseline)
	if err := store.SetRunConfig(ctx, tx, runID, configHash, in.Mode, switches.Names()); err != nil {
		return Result{}, fmt.Errorf("record run config: %w", err)
	}
	mapper := mapping.NewMapper(payRules, setRules, switches)

	settlementRows := make([]store.SourceRow, 0, 1+len(settle.Components))
	settlementRows = append(settlementRows, scopedRow(setDigest.name, settle.Header, headerID))
	for _, l := range settle.Components {
		row, err := mappedRow(mapper, setDigest.name, l, headerID)
		if err != nil {
			return Result{}, err
		}
		settlementRows = append(settlementRows, row)
	}
	paymentRows := make([]store.SourceRow, 0, len(pay.Lines))
	for _, l := range pay.Lines {
		row, err := mappedRow(mapper, payDigest.name, l, headerID)
		if err != nil {
			return Result{}, err
		}
		paymentRows = append(paymentRows, row)
	}

	if err := store.RecordFile(ctx, tx, runID, store.FileRecord{
		Source: source.Payments, FileName: payDigest.name, SHA256: payDigest.sha,
		PhysicalLines: payDigest.lines, StoredLines: len(paymentRows), MalformedLines: len(pay.Errors),
		Preamble: pay.Preamble,
	}); err != nil {
		return Result{}, fmt.Errorf("record payments file: %w", err)
	}
	if err := store.RecordFile(ctx, tx, runID, store.FileRecord{
		Source: source.Settlement, FileName: setDigest.name, SHA256: setDigest.sha,
		PhysicalLines: setDigest.lines, StoredLines: len(settlementRows), MalformedLines: len(settle.Errors),
	}); err != nil {
		return Result{}, fmt.Errorf("record settlement file: %w", err)
	}
	if err := store.WriteSource(ctx, tx, runID, source.Settlement, settlementRows); err != nil {
		return Result{}, fmt.Errorf("store settlement lines: %w", err)
	}
	if err := store.WriteSource(ctx, tx, runID, source.Payments, paymentRows); err != nil {
		return Result{}, fmt.Errorf("store payments lines: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("commit: %w", err)
	}
	return Result{
		SettlementID:    headerID,
		PaymentsLines:   len(paymentRows),
		SettlementLines: len(settlementRows),
		Malformed:       len(malformed),
		ConfigHash:      configHash,
		Mode:            in.Mode,
	}, nil
}

// firstInScope returns the first malformed line that blocks the run.
func firstInScope(lines []store.MalformedLine) store.MalformedLine {
	for _, l := range lines {
		if l.InScope {
			return l
		}
	}
	return store.MalformedLine{}
}

// scopedRow decides a line's scope. Scope is the Settlement ID from the
// Settlement report header, plus status Released for payments lines.
func scopedRow(fileName string, l source.Line, headerID string) store.SourceRow {
	r := store.SourceRow{FileName: fileName, Line: l}
	switch {
	case l.Kind == source.KindSettlementHeader:
		r.ScopeReason = "Settlement header (reference total)"
	case l.SettlementID != headerID:
		r.ScopeReason = "Other settlement"
	case l.Source == source.Settlement:
		r.InScope, r.ScopeReason = true, "Settlement component"
	case l.Status == "Released":
		r.InScope, r.ScopeReason = true, "Released"
	case l.Status == "Deferred":
		r.ScopeReason = "Deferred"
	default:
		r.ScopeReason = "Status " + l.Status
	}
	return r
}

func readPayments(path string) (source.PaymentsReport, error) {
	f, err := os.Open(path)
	if err != nil {
		return source.PaymentsReport{}, err
	}
	defer f.Close()
	return source.ReadPayments(f)
}

func readSettlement(path string) (source.SettlementReport, error) {
	f, err := os.Open(path)
	if err != nil {
		return source.SettlementReport{}, err
	}
	defer f.Close()
	return source.ReadSettlement(f)
}

type digest struct {
	name  string
	sha   string
	lines int
}

// digestFile hashes a file and counts its physical lines, as wc -l would
// (plus one for a final line without a newline).
func digestFile(path string) (digest, error) {
	f, err := os.Open(path)
	if err != nil {
		return digest{}, err
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, 64*1024)
	var lines int
	var last byte
	var size int64
	for {
		n, err := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			h.Write(chunk)
			lines += bytes.Count(chunk, []byte{'\n'})
			last = chunk[n-1]
			size += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return digest{}, err
		}
	}
	if size > 0 && last != '\n' {
		lines++
	}
	return digest{name: filepath.Base(path), sha: hex.EncodeToString(h.Sum(nil)), lines: lines}, nil
}
