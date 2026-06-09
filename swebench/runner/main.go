// Package main provides a runner to test goagent on random SWE-bench samples.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Instance struct {
	InstanceID       string `json:"instance_id"`
	Repo             string `json:"repo"`
	BaseCommit       string `json:"base_commit"`
	ProblemStatement string `json:"problem_statement"`
	HintsText        string `json:"hints_text"`
}

type Prediction struct {
	InstanceID      string `json:"instance_id"`
	ModelNameOrPath string `json:"model_name_or_path"`
	ModelPatch      string `json:"model_patch"`
}

type CaseStatus struct {
	InstanceID          string `json:"instance_id"`
	GeneratedPrediction bool   `json:"generated_prediction"`
	HasPatch            bool   `json:"has_patch"`
	VerifyStatus        string `json:"verify_status"`
	RunResult           string `json:"run_result"`
	StopReason          string `json:"stop_reason"`
	ErrorMessage        string `json:"error_message,omitempty"`
}

type caseMetrics struct {
	RunResult    string `json:"run_result"`
	StopReason   string `json:"stop_reason"`
	VerifyStatus string `json:"verify_status"`
}

func main() {
	datasetPath := flag.String("dataset", "", "Path to SWE-bench dataset directory")
	numSamples := flag.Int("n", 3, "Number of random samples to test when --instances is empty")
	instanceIDs := flag.String("instances", "", "Comma-separated instance IDs to run instead of random sampling")
	outputPath := flag.String("output", "predictions.jsonl", "Output JSONL file")
	flag.Parse()

	if *datasetPath == "" {
		fmt.Fprintf(os.Stderr, "Error: --dataset flag is required\n")
		flag.Usage()
		os.Exit(1)
	}

	// Load dataset
	instances, err := loadDataset(*datasetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading dataset: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Loaded %d instances from dataset\n", len(instances))

	samples, err := selectInstances(instances, *instanceIDs, *numSamples)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error selecting instances: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(*instanceIDs) == "" {
		fmt.Printf("Selected %d random samples\n", len(samples))
	} else {
		fmt.Printf("Selected %d requested instances\n", len(samples))
	}

	// Open output file
	outFile, err := os.Create(*outputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output file: %v\n", err)
		os.Exit(1)
	}
	defer outFile.Close()

	// Process each sample
	successCount := 0
	for i, instance := range samples {
		status := CaseStatus{InstanceID: instance.InstanceID, RunResult: "command_failed"}
		fmt.Printf("\n[%d/%d] Processing %s...\n", i+1, len(samples), instance.InstanceID)
		fmt.Printf("  Repo: %s\n", instance.Repo)
		fmt.Printf("  Problem: %s\n", truncate(instance.ProblemStatement, 100))

		// Write instance to temp file
		tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("instance_%d.json", i))
		instanceData, _ := json.Marshal(instance)
		if err := os.WriteFile(tmpFile, instanceData, 0644); err != nil {
			fmt.Printf("  ERROR: Failed to write temp file: %v\n", err)
			status.ErrorMessage = err.Error()
			writeRunnerStatus(status)
			continue
		}

		// Run adapter. The adapter writes progress logs to stderr and the final
		// prediction JSON to stdout, so keep the streams separate.
		cmd := adapterCommand(tmpFile)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()

		if err != nil {
			fmt.Printf("  ERROR: Adapter failed: %v\n", err)
			status.ErrorMessage = err.Error()
			if stderr.Len() > 0 {
				fmt.Printf("  Stderr: %s\n", stderr.String())
			}
			if stdout.Len() > 0 {
				fmt.Printf("  Stdout: %s\n", stdout.String())
			}
			if metrics, ok := loadCaseMetrics(); ok {
				applyMetricsToStatus(&status, metrics)
			}
			writeRunnerStatus(status)
			continue
		}

		// Parse prediction
		var pred Prediction
		if err := json.Unmarshal(lastNonEmptyLine(stdout.Bytes()), &pred); err != nil {
			fmt.Printf("  ERROR: Failed to parse prediction: %v\n", err)
			status.RunResult = "case_failed"
			status.ErrorMessage = err.Error()
			if stderr.Len() > 0 {
				fmt.Printf("  Stderr: %s\n", stderr.String())
			}
			fmt.Printf("  Stdout: %s\n", stdout.String())
			if metrics, ok := loadCaseMetrics(); ok {
				applyMetricsToStatus(&status, metrics)
			}
			writeRunnerStatus(status)
			continue
		}
		status.GeneratedPrediction = true
		status.HasPatch = pred.ModelPatch != ""
		status.RunResult = "case_success"
		if metrics, ok := loadCaseMetrics(); ok {
			applyMetricsToStatus(&status, metrics)
		}

		// Write to output file
		predJSON, _ := json.Marshal(pred)
		fmt.Fprintf(outFile, "%s\n", predJSON)

		if status.RunResult == "case_success" {
			fmt.Printf("  SUCCESS: Generated passing patch (%d bytes)\n", len(pred.ModelPatch))
			successCount++
		} else {
			fmt.Printf("  RESULT: Generated patch (%d bytes), run_result=%s verify=%s\n", len(pred.ModelPatch), status.RunResult, status.VerifyStatus)
		}
		writeRunnerStatus(status)

		// Cleanup
		os.Remove(tmpFile)
	}

	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Total samples: %d\n", len(samples))
	fmt.Printf("Successful: %d\n", successCount)
	fmt.Printf("Failed: %d\n", len(samples)-successCount)
	fmt.Printf("Output written to: %s\n", *outputPath)
}

func loadCaseMetrics() (caseMetrics, bool) {
	runDir := os.Getenv("SWE_RUN_DIR")
	if runDir == "" {
		return caseMetrics{}, false
	}
	data, err := os.ReadFile(filepath.Join(runDir, "metrics.json"))
	if err != nil {
		return caseMetrics{}, false
	}
	var metrics caseMetrics
	if err := json.Unmarshal(data, &metrics); err != nil {
		return caseMetrics{}, false
	}
	return metrics, true
}

func applyMetricsToStatus(status *CaseStatus, metrics caseMetrics) {
	if metrics.RunResult != "" {
		status.RunResult = metrics.RunResult
	}
	status.StopReason = metrics.StopReason
	status.VerifyStatus = metrics.VerifyStatus
}

func writeRunnerStatus(status CaseStatus) {
	runDir := os.Getenv("SWE_RUN_DIR")
	if runDir == "" {
		return
	}
	data, err := json.Marshal(status)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(runDir, "runner_status.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

func selectInstances(instances []Instance, ids string, n int) ([]Instance, error) {
	if strings.TrimSpace(ids) == "" {
		return randomSample(instances, n), nil
	}

	byID := make(map[string]Instance, len(instances))
	for _, inst := range instances {
		byID[inst.InstanceID] = inst
	}

	var selected []Instance
	for _, id := range strings.Split(ids, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		inst, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("instance %q not found", id)
		}
		selected = append(selected, inst)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("--instances did not contain any instance IDs")
	}
	return selected, nil
}

func adapterCommand(instanceFile string) *exec.Cmd {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return exec.Command("go", "run", "../adapter", instanceFile)
	}

	adapterDir := filepath.Join(filepath.Dir(file), "..", "adapter")
	bin := filepath.Join(adapterDir, "adapter")
	if info, err := os.Stat(bin); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
		cmd := exec.Command(bin, instanceFile)
		cmd.Dir = adapterDir
		return cmd
	}

	cmd := exec.Command("go", "run", ".", instanceFile)
	cmd.Dir = adapterDir
	return cmd
}

func loadDataset(path string) ([]Instance, error) {
	// Accept direct file path or directory
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		jsonFile := filepath.Join(path, "dataset.json")
		return loadFromJSON(jsonFile)
	}

	return loadFromJSON(path)
}

func loadFromJSON(path string) ([]Instance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var instances []Instance
	// Handle both array and JSONL formats
	if err := json.Unmarshal(data, &instances); err != nil {
		// Try JSONL format
		lines := splitLines(string(data))
		instances = make([]Instance, 0, len(lines))
		for _, line := range lines {
			if line == "" {
				continue
			}
			var inst Instance
			if err := json.Unmarshal([]byte(line), &inst); err != nil {
				return nil, fmt.Errorf("failed to parse JSONL line: %w", err)
			}
			instances = append(instances, inst)
		}
	}

	return instances, nil
}

func randomSample(instances []Instance, n int) []Instance {
	if n >= len(instances) {
		return instances
	}

	indices := rand.Perm(len(instances))[:n]
	samples := make([]Instance, n)
	for i, idx := range indices {
		samples[i] = instances[idx]
	}
	return samples
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func lastNonEmptyLine(data []byte) []byte {
	lines := bytes.Split(data, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) > 0 {
			return line
		}
	}
	return nil
}
