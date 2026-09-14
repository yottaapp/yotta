package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/storage"
	storagemigrate "github.com/yottaapp/yotta/internal/storage/migrate"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

func TestParseOptionsAcceptsEveryCommandAndResolvesPaths(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"validate", "compile", "inspect", "run"} {
		command := command
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			opt, err := parseOptions([]string{
				"--data-root", root,
				"--principal", "test-principal",
				"--timeout", "30s",
				command, "argument",
			})
			if err != nil {
				t.Fatalf("parse options: %v", err)
			}
			if opt.command != command || opt.argument != "argument" || opt.principal != "test-principal" || opt.timeout != 30*time.Second {
				t.Fatalf("unexpected options: %#v", opt)
			}
			if opt.dataRoot != root || !filepath.IsAbs(opt.executable) {
				t.Fatalf("paths were not preserved/resolved: %#v", opt)
			}
		})
	}
}

func TestParseOptionsRejectsInvalidInvocation(t *testing.T) {
	t.Parallel()
	for _, arguments := range [][]string{
		{},
		{"validate"},
		{"health", "unexpected"},
		{"unknown", "workflow"},
		{"--timeout", "0s", "inspect", "workflow"},
		{"--timeout", "not-a-duration", "inspect", "workflow"},
	} {
		if _, err := parseOptions(arguments); err == nil {
			t.Fatalf("expected %q to be rejected", strings.Join(arguments, " "))
		}
	}
}

func TestRunHealthIsReadOnlyAndRedactsTheRootByDefault(t *testing.T) {
	root := filepath.Join(t.TempDir(), "profile")
	runtime, err := buildRuntime(options{
		dataRoot:   root,
		executable: filepath.Join(root, "Yotta.CLI.exe"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runtime.Close(ctx); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := run([]string{"--data-root", root, "health"}, &output); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Root      string `json:"root"`
		Databases struct {
			Healthy bool `json:"healthy"`
			Content struct {
				Present bool `json:"present"`
			} `json:"content"`
			Runs struct {
				Present bool `json:"present"`
			} `json:"runs"`
		} `json:"databases"`
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Root != "<redacted>" {
		t.Fatalf("health output exposed or omitted root: %s", output.String())
	}
	if !report.Databases.Healthy || !report.Databases.Content.Present || !report.Databases.Runs.Present {
		t.Fatalf("health output omitted catalog foundation: %s", output.String())
	}
	if err := run([]string{"--data-root", root, "--show-path", "health"}, &output); err != nil {
		t.Fatal(err)
	}
}

func TestRunMigrationPlanIsReadOnlyAndApplyCommitsLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "layout-1")
	roots, err := storage.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(roots.Config, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		roots.ManifestFile(),
		[]byte("{\n  \"format\": \"yotta.storage-root\",\n  \"version\": \"1\"\n}"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run([]string{"--data-root", root, "migrate", "plan"}, &output); err != nil {
		t.Fatal(err)
	}
	var plan storagemigrate.Plan
	if err := json.Unmarshal(output.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.From != "1" || plan.To != "2" {
		t.Fatalf("migration plan = %#v", plan)
	}
	if _, err := os.Stat(filepath.Join(roots.Migrations, "layout-1-to-2")); !os.IsNotExist(err) {
		t.Fatalf("plan wrote migration state: %v", err)
	}
	output.Reset()
	if err := run([]string{"--data-root", root, "migrate", "apply"}, &output); err != nil {
		t.Fatal(err)
	}
	var result storagemigrate.Result
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Journal.State != storagemigrate.StateCommitted {
		t.Fatalf("migration result = %#v", result)
	}
	health, err := storage.Inspect(context.Background(), storage.InspectOptions{Root: root})
	if err != nil || !health.Supported || health.LayoutVersion != storage.LayoutVersion {
		t.Fatalf("migrated storage health = %#v, %v", health, err)
	}
}

func TestBuildRuntimeStartsWithAnEmptyInstallationSet(t *testing.T) {
	root := t.TempDir()
	runtime, err := buildRuntime(options{
		dataRoot:   root,
		executable: filepath.Join(root, "Yotta.CLI.exe"),
	})
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runtime.Workflow.Application.Start(ctx); err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	if err := runtime.Close(ctx); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
}

func TestCompileResultViewAlwaysEmitsDiagnostics(t *testing.T) {
	view := compileResultView(compiler.CompileResult{})
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"diagnostics":null}` {
		t.Fatalf("unexpected compile view: %s", raw)
	}
}

func TestRunValidateStrictlyRejectsLegacySource(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "legacy.json")
	if err := os.WriteFile(sourcePath, []byte(`{"format":"yotta.workflow","version":"5"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := run([]string{
		"--data-root", filepath.Join(root, "data"),
		"--timeout", "10s",
		"validate", sourcePath,
	}, &output)
	if err == nil {
		t.Fatal("expected legacy Workflow source to be rejected")
	}
	var view compileView
	if decodeErr := json.Unmarshal(output.Bytes(), &view); decodeErr != nil {
		t.Fatalf("decode diagnostics: %v; output=%s", decodeErr, output.String())
	}
	if len(view.Diagnostics) == 0 {
		t.Fatalf("expected strict validation diagnostics, got %s", output.String())
	}
}

func TestRunWorkflowCommandsRejectMissingWorkflow(t *testing.T) {
	for _, command := range []string{"compile", "inspect", "run"} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			var output bytes.Buffer
			err := run([]string{
				"--data-root", filepath.Join(root, "data"),
				"--timeout", "10s",
				command, "missing-workflow",
			}, &output)
			if err == nil {
				t.Fatalf("expected %s to reject a missing workflow", command)
			}
			if command != "inspect" && !strings.Contains(output.String(), "diagnostics") {
				t.Fatalf("expected %s to emit a structured compile view, got %s", command, output.String())
			}
		})
	}
}
