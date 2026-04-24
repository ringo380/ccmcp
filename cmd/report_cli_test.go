package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIReportSnapshotJSON(t *testing.T) {
	home := setupSandbox(t)
	out, err := runCLI(t, home, "report", "snapshot")
	if err != nil {
		t.Fatalf("report snapshot err: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"generatedAt"`) {
		t.Errorf("expected JSON snapshot with generatedAt; got:\n%s", out)
	}
	if !strings.Contains(out, `"userMcps"`) {
		t.Errorf("expected userMcps in snapshot; got:\n%s", out)
	}
}

func TestCLIReportSnapshotMD(t *testing.T) {
	home := setupSandbox(t)
	out, err := runCLI(t, home, "report", "--format", "md", "snapshot")
	if err != nil {
		t.Fatalf("report snapshot md err: %v\n%s", err, out)
	}
	if !strings.Contains(out, "# ccmcp Snapshot") {
		t.Errorf("expected Markdown header; got:\n%s", out)
	}
}

func TestCLIReportSnapshotCSV(t *testing.T) {
	home := setupSandbox(t)
	out, err := runCLI(t, home, "report", "--format", "csv", "snapshot")
	if err != nil {
		t.Fatalf("report snapshot csv err: %v\n%s", err, out)
	}
	if !strings.Contains(out, "category,name,detail") {
		t.Errorf("expected CSV header; got:\n%s", out)
	}
}

func TestCLIReportSnapshotOutFile(t *testing.T) {
	home := setupSandbox(t)
	outFile := filepath.Join(t.TempDir(), "snap.json")
	_, err := runCLI(t, home, "report", "snapshot", "--out", outFile)
	if err != nil {
		t.Fatalf("report snapshot --out err: %v", err)
	}
	b, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("output file not created: %v", err)
	}
	var snap map[string]any
	if err := json.Unmarshal(b, &snap); err != nil {
		t.Fatalf("output file not valid JSON: %v", err)
	}
	if snap["generatedAt"] == nil {
		t.Error("snapshot JSON missing generatedAt")
	}
}

func TestCLIReportSweepJSON(t *testing.T) {
	home := setupSandbox(t)
	out, err := runCLI(t, home, "report", "sweep")
	if err != nil {
		t.Fatalf("report sweep err: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"generatedAt"`) || !strings.Contains(out, `"projects"`) {
		t.Errorf("expected sweep JSON; got:\n%s", out)
	}
}

func TestCLIReportSweepMD(t *testing.T) {
	home := setupSandbox(t)
	out, err := runCLI(t, home, "report", "--format", "md", "sweep")
	if err != nil {
		t.Fatalf("report sweep md err: %v\n%s", err, out)
	}
	if !strings.Contains(out, "# ccmcp Sweep") {
		t.Errorf("expected Sweep Markdown header; got:\n%s", out)
	}
}

func TestCLIReportDriftRequiresFrom(t *testing.T) {
	home := setupSandbox(t)
	_, err := runCLI(t, home, "report", "drift")
	if err == nil {
		t.Error("expected error when --from is missing")
	}
}

func TestCLIReportDrift(t *testing.T) {
	home := setupSandbox(t)
	// Write a snapshot to disk
	snapFile := filepath.Join(t.TempDir(), "baseline.json")
	_, err := runCLI(t, home, "report", "snapshot", "--out", snapFile)
	if err != nil {
		t.Fatalf("snapshot for drift baseline: %v", err)
	}
	// Drift against itself — should be clean
	out, err := runCLI(t, home, "report", "--format", "md", "drift", "--from", snapFile)
	if err != nil {
		t.Fatalf("report drift err: %v\n%s", err, out)
	}
	if !strings.Contains(out, "# ccmcp Drift") {
		t.Errorf("expected Drift Markdown header; got:\n%s", out)
	}
	if !strings.Contains(out, "No changes detected") {
		t.Errorf("drift against itself should report no changes; got:\n%s", out)
	}
}

func TestCLIReportAuditJSON(t *testing.T) {
	home := setupSandbox(t)
	out, err := runCLI(t, home, "report", "audit")
	if err != nil {
		t.Fatalf("report audit err: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"generatedAt"`) {
		t.Errorf("expected audit JSON; got:\n%s", out)
	}
}

func TestCLIReportAuditMD(t *testing.T) {
	home := setupSandbox(t)
	out, err := runCLI(t, home, "report", "--format", "md", "audit")
	if err != nil {
		t.Fatalf("report audit md err: %v\n%s", err, out)
	}
	if !strings.Contains(out, "# ccmcp Audit") {
		t.Errorf("expected Audit Markdown header; got:\n%s", out)
	}
}
