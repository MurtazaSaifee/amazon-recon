package pipelinetest

import (
	"context"
	"path/filepath"
	"testing"

	"amazonrecon/internal/ingest"
	"amazonrecon/internal/recon"
)

func TestRunModeIsRecorded(t *testing.T) {
	ctx := context.Background()
	pool := newPipelineDB(t)
	res, err := ingest.Run(ctx, pool, ingest.Inputs{
		PaymentsPath:   filepath.Join(fixtures, "synthetic_payments.csv"),
		SettlementPath: filepath.Join(fixtures, "synthetic_settlement.txt"),
		Mode:           "baseline",
	})
	if err != nil {
		t.Fatalf("ingest in baseline mode: %v", err)
	}
	rep, err := recon.Build(ctx, pool, res.RunID)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rep.Mode != "baseline" {
		t.Errorf("mode = %q, want baseline", rep.Mode)
	}

	if _, err := ingest.Run(ctx, pool, ingest.Inputs{
		PaymentsPath:   filepath.Join(fixtures, "synthetic_payments.csv"),
		SettlementPath: filepath.Join(fixtures, "synthetic_settlement.txt"),
		Mode:           "sometimes",
	}); err == nil {
		t.Error("an unknown mode was accepted, want an error")
	}
}
