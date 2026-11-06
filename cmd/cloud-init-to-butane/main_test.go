package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_HelpOrNoInput(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "-in", "")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit code when -in is empty")
	}
	if !strings.Contains(string(output), "Usage: cloud-init-to-butane") {
		t.Errorf("expected usage output, got: %s", string(output))
	}
}

func TestCLI_TranspileCAPIWorker(t *testing.T) {
	testDataPath := filepath.Join("..", "..", "testdata", "capi-worker.yaml")
	if _, err := os.Stat(testDataPath); os.IsNotExist(err) {
		t.Skip("testdata/capi-worker.yaml not found, skipping")
	}

	cmd := exec.Command("go", "run", ".", "-in", testDataPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed with error %v: %s", err, string(output))
	}

	outStr := string(output)
	if !strings.Contains(outStr, "variant: flatcar") {
		t.Errorf("expected output to contain 'variant: flatcar', got: %s", outStr)
	}
	if !strings.Contains(outStr, "version: 1.0.0") {
		t.Errorf("expected output to contain 'version: 1.0.0', got: %s", outStr)
	}
}
