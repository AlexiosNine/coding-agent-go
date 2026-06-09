package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSelectInstancesExplicitIDs(t *testing.T) {
	instances := []Instance{
		{InstanceID: "django__django-11179"},
		{InstanceID: "sympy__sympy-11400"},
		{InstanceID: "pytest-dev__pytest-11143"},
	}

	selected, err := selectInstances(instances, "sympy__sympy-11400, pytest-dev__pytest-11143", 3)
	if err != nil {
		t.Fatalf("select instances: %v", err)
	}
	if len(selected) != 2 {
		t.Fatalf("expected 2 selected instances, got %d", len(selected))
	}
	if selected[0].InstanceID != "sympy__sympy-11400" || selected[1].InstanceID != "pytest-dev__pytest-11143" {
		t.Fatalf("selection order mismatch: %#v", selected)
	}
}

func TestSelectInstancesMissingID(t *testing.T) {
	instances := []Instance{{InstanceID: "django__django-11179"}}
	if _, err := selectInstances(instances, "missing__case-1", 3); err == nil {
		t.Fatal("expected missing instance error")
	}
}

func TestLastNonEmptyLine(t *testing.T) {
	output := []byte("progress log on stdout\n\n{\"instance_id\":\"sympy__sympy-11400\",\"model_patch\":\"diff\"}\n")

	var pred Prediction
	if err := json.Unmarshal(lastNonEmptyLine(output), &pred); err != nil {
		t.Fatalf("parse prediction: %v", err)
	}
	if pred.InstanceID != "sympy__sympy-11400" || pred.ModelPatch != "diff" {
		t.Fatalf("unexpected prediction: %#v", pred)
	}
}

func TestLoadCaseMetricsAndWriteRunnerStatus(t *testing.T) {
	runDir := t.TempDir()
	t.Setenv("SWE_RUN_DIR", runDir)

	metricsJSON := []byte(`{"run_result":"case_failed","stop_reason":"success_text","verify_status":"failed"}`)
	if err := os.WriteFile(filepath.Join(runDir, "metrics.json"), metricsJSON, 0644); err != nil {
		t.Fatalf("write metrics: %v", err)
	}

	metrics, ok := loadCaseMetrics()
	if !ok {
		t.Fatal("expected metrics to load")
	}

	status := CaseStatus{InstanceID: "case", GeneratedPrediction: true, HasPatch: true, RunResult: "case_success"}
	applyMetricsToStatus(&status, metrics)
	if status.RunResult != "case_failed" || status.VerifyStatus != "failed" || status.StopReason != "success_text" {
		t.Fatalf("metrics not applied: %#v", status)
	}

	writeRunnerStatus(status)
	data, err := os.ReadFile(filepath.Join(runDir, "runner_status.jsonl"))
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	var got CaseStatus
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if got.RunResult != "case_failed" || !got.GeneratedPrediction || !got.HasPatch {
		t.Fatalf("unexpected status: %#v", got)
	}
}
