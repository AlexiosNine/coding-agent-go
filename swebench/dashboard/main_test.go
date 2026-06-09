package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanRunsReadsMetricsAndArtifacts(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "20260609_130000_case__one")
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatalf("mkdir run: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "metrics.json"), []byte(`{
		"instance_id":"case__one",
		"run_result":"case_success",
		"verify_status":"passed",
		"turns":4,
		"tool_calls":3,
		"patch_lines":2
	}`), 0644); err != nil {
		t.Fatalf("write metrics: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte("{}\n{}\n"), 0644); err != nil {
		t.Fatalf("write events: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "prediction_patch.diff"), []byte("diff\n"), 0644); err != nil {
		t.Fatalf("write patch: %v", err)
	}

	runs, err := scanRuns([]string{root})
	if err != nil {
		t.Fatalf("scan runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	run := runs[0]
	if run.InstanceID != "case__one" || run.State != "case_success" || run.Metrics.VerifyStatus != "passed" {
		t.Fatalf("unexpected run: %#v", run)
	}
	if !run.HasPatch || !run.HasEvents || run.EventCount != 2 {
		t.Fatalf("artifact fields not populated: %#v", run)
	}
}

func TestSafeArtifactPathRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "20260609_130000_case__one")
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatalf("mkdir run: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "metrics.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	srv := &server{roots: []string{root}}
	if _, err := srv.safeArtifactPath(runDir, "metrics.json"); err != nil {
		t.Fatalf("expected artifact path to be allowed: %v", err)
	}
	if _, err := srv.safeArtifactPath(runDir, "../metrics.json"); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
	if _, err := srv.safeArtifactPath(filepath.Dir(root), "metrics.json"); err == nil {
		t.Fatal("expected outside root to be rejected")
	}
}
