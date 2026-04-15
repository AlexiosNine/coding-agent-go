// Package main provides a runner to test goagent on random SWE-bench samples.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
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

func main() {
	datasetPath := flag.String("dataset", "", "Path to SWE-bench dataset directory")
	numSamples := flag.Int("n", 5, "Number of random samples to test")
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

	// Random sample
	samples := randomSample(instances, *numSamples)
	fmt.Printf("Selected %d random samples\n", len(samples))

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
		fmt.Printf("\n[%d/%d] Processing %s...\n", i+1, len(samples), instance.InstanceID)
		fmt.Printf("  Repo: %s\n", instance.Repo)
		fmt.Printf("  Problem: %s\n", truncate(instance.ProblemStatement, 100))

		// Write instance to temp file
		tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("instance_%d.json", i))
		instanceData, _ := json.Marshal(instance)
		if err := os.WriteFile(tmpFile, instanceData, 0644); err != nil {
			fmt.Printf("  ERROR: Failed to write temp file: %v\n", err)
			continue
		}

		// Run adapter
		cmd := exec.Command("go", "run", "adapter.go", tmpFile)
		cmd.Dir = filepath.Dir(os.Args[0])
		output, err := cmd.CombinedOutput()

		if err != nil {
			fmt.Printf("  ERROR: Adapter failed: %v\n", err)
			fmt.Printf("  Output: %s\n", string(output))
			continue
		}

		// Parse prediction
		var pred Prediction
		if err := json.Unmarshal(output, &pred); err != nil {
			fmt.Printf("  ERROR: Failed to parse prediction: %v\n", err)
			fmt.Printf("  Output: %s\n", string(output))
			continue
		}

		// Write to output file
		predJSON, _ := json.Marshal(pred)
		fmt.Fprintf(outFile, "%s\n", predJSON)

		fmt.Printf("  SUCCESS: Generated patch (%d bytes)\n", len(pred.ModelPatch))
		successCount++

		// Cleanup
		os.Remove(tmpFile)
	}

	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Total samples: %d\n", len(samples))
	fmt.Printf("Successful: %d\n", successCount)
	fmt.Printf("Failed: %d\n", len(samples)-successCount)
	fmt.Printf("Output written to: %s\n", *outputPath)
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
