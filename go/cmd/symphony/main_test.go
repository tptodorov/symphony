package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"strings"
	"testing"
)

func TestParseArgsDefaultsWorkflow(t *testing.T) {
	opts, err := parseArgs(nil, "symphony", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if opts.WorkflowPath != "WORKFLOW.md" {
		t.Fatalf("workflow=%q", opts.WorkflowPath)
	}
	if opts.WorkDir != "" {
		t.Fatalf("workdir=%q", opts.WorkDir)
	}
}

func TestSetRuntimeEnvExportsEffectiveWorkdir(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(old); err != nil {
			t.Fatal(err)
		}
	}()
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	want, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SYMPHONY_WORKDIR", "")
	if err := setRuntimeEnv(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("SYMPHONY_WORKDIR"); got != want {
		t.Fatalf("SYMPHONY_WORKDIR=%q, want %q", got, want)
	}
}

func TestParseArgsWorkdirAndPositionalWorkflow(t *testing.T) {
	opts, err := parseArgs([]string{"-workdir", "/repo", "-port", "0", "experiments/WORKFLOW.md"}, "symphony", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if opts.WorkDir != "/repo" {
		t.Fatalf("workdir=%q", opts.WorkDir)
	}
	if opts.WorkflowPath != "experiments/WORKFLOW.md" {
		t.Fatalf("workflow=%q", opts.WorkflowPath)
	}
	if !opts.PortSet || opts.Port != 0 {
		t.Fatalf("port presence not preserved: %+v", opts)
	}
}

func TestParseArgsRejectsMultipleWorkflowPaths(t *testing.T) {
	_, err := parseArgs([]string{"one.md", "two.md"}, "symphony", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "at most one workflow path") {
		t.Fatalf("expected workflow path error, got %v", err)
	}
}

func TestParseArgsHelpShowsPositionalWorkflow(t *testing.T) {
	var out bytes.Buffer
	_, err := parseArgs([]string{"-help"}, "./symphony", &out)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
	help := out.String()
	if !strings.Contains(help, "Usage of ./symphony [flags] [path-to-WORKFLOW.md]:") {
		t.Fatalf("help missing positional workflow: %s", help)
	}
	if !strings.Contains(help, "-workdir") {
		t.Fatalf("help missing workdir flag: %s", help)
	}
}
