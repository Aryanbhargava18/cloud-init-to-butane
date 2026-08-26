package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Aryanbhargava18/cloud-init-to-butane/pkg/parser"
	"github.com/Aryanbhargava18/cloud-init-to-butane/pkg/transpiler"
	"gopkg.in/yaml.v3"

	"github.com/coreos/butane/config/common"
	flatcar "github.com/coreos/butane/config/flatcar/v1_0"
)

func main() {
	inFile := flag.String("in", "", "Input cloud-init YAML file")
	outFile := flag.String("out", "-", "Output Butane YAML file (- for stdout)")
	targetVersion := flag.String("version", "1.0.0", "Target Butane version (e.g. 1.0.0, 1.1.0)")
	flag.Parse()

	if *inFile == "" {
		fmt.Fprintln(os.Stderr, "Usage: cloud-init-to-butane -in <cloud-init.yaml> [-out <butane.yaml>] [-version 1.0.0]")
		os.Exit(1)
	}

	var inData []byte
	var err error

	if *inFile == "-" {
		inData, err = io.ReadAll(os.Stdin)
	} else {
		inData, err = os.ReadFile(*inFile)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read input: %v\n", err)
		os.Exit(1)
	}

	cfg, root, _, err := parser.Parse(inData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Parse error: %v\n", err)
		os.Exit(1)
	}

	outCfg, err := transpiler.Transpile(cfg, root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Transpile error: %v\n", err)
		os.Exit(1)
	}
	outCfg.Variant = "flatcar"
	outCfg.Version = *targetVersion

	outData, err := yaml.Marshal(outCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to marshal output: %v\n", err)
		os.Exit(1)
	}

	// Verify the generated YAML using Butane's core transpiler.
	// We run it through config/flatcar/v1_0.ToIgn3_3Bytes to ensure it is valid.
	_, verifyReport, err := flatcar.ToIgn3_3Bytes(outData, common.TranslateBytesOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Verification failed! Generated Butane is invalid: %v\n", err)
		os.Exit(1)
	}
	if verifyReport.IsFatal() {
		fmt.Fprintf(os.Stderr, "Verification fatal: %s\n", verifyReport.String())
		os.Exit(1)
	}

	if *outFile == "" || *outFile == "-" {
		fmt.Print(string(outData))
	} else {
		if err := os.WriteFile(*outFile, outData, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write output: %v\n", err)
			os.Exit(1)
		}
	}
}

// improve errors

// improve errors
