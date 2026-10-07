package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMotionDeprecationAuditCLI(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "motion-deprecation-audit")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = "."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	cases := []struct {
		name          string
		auditDate     string
		declaredExtra bool
		target        string
		wantGate      string
		wantExit      int
	}{
		{name: "pass audit only", auditDate: "2098-01-02", target: "short_phrase", wantGate: "PASS_AUDIT_ONLY"},
		{name: "undeclared consumer blocks", auditDate: "2098-01-02", target: "short_phrase", declaredExtra: true, wantGate: "BLOCKED", wantExit: 2},
		{name: "incompatible target blocks", auditDate: "2098-01-02", target: "image", wantGate: "BLOCKED", wantExit: 2},
		{name: "expired window blocks", auditDate: "2098-03-02", target: "short_phrase", wantGate: "BLOCKED", wantExit: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "contracts"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "evidence"), 0o755); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{proposalSchema, reportSchemaPath} {
				data, err := os.ReadFile(filepath.Join(moduleRoot, path))
				if err != nil {
					t.Fatalf("read schema %s: %v", path, err)
				}
				if err := os.WriteFile(filepath.Join(root, path), data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			proposalDoc := proposal{
				Schema: "renderinggen.motion-deprecation-proposal.v1", Version: 1,
				MotionID:     "typewriter_modern_01_monospace_block_cursor",
				DeprecatedOn: "2098-01-01", RemoveAfter: "2098-03-02",
				Reason: "isolated command test", ReplacementID: "typewriter_modern_02_kinetic_scramble",
				RequiredTargets: []string{tc.target}, Owner: "test owner", CompatibilityWindowDays: 60,
				Consumers: []string{"consumer.txt"},
			}
			if tc.declaredExtra {
				proposalDoc.Consumers = append(proposalDoc.Consumers, "not-present.txt")
			}
			proposalBytes, err := json.MarshalIndent(proposalDoc, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			proposalPath := filepath.Join(root, "evidence", "proposal.json")
			if err := os.WriteFile(proposalPath, append(proposalBytes, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "consumer.txt"), []byte(proposalDoc.MotionID+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			reportPath := filepath.Join(root, "evidence", "report.json")
			cmd := exec.Command(binary, "-proposal", proposalPath, "-root", root, "-audit-date", tc.auditDate, "-output", reportPath)
			output, runErr := cmd.CombinedOutput()
			if runErr != nil {
				if exitErr, ok := runErr.(*exec.ExitError); !ok || exitErr.ExitCode() != tc.wantExit {
					t.Fatalf("CLI exit = %v, want code %d\n%s", runErr, tc.wantExit, output)
				}
			} else if tc.wantExit != 0 {
				t.Fatalf("CLI exited successfully, want code %d", tc.wantExit)
			}
			data, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatalf("read generated report: %v\n%s", err, output)
			}
			var got report
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("decode report: %v", err)
			}
			if got.Gate != tc.wantGate {
				t.Fatalf("gate = %q, want %q", got.Gate, tc.wantGate)
			}
			if tc.name == "undeclared consumer blocks" && len(got.MissingConsumers) != 1 {
				t.Fatalf("missing consumers = %v, want the declared nonexistent consumer", got.MissingConsumers)
			}
			if tc.name == "incompatible target blocks" && got.TargetsCompatible {
				t.Fatal("incompatible image target was reported compatible")
			}
			if tc.name == "expired window blocks" && !strings.Contains(strings.Join(got.Limitations, " "), "compatibility window") {
				t.Fatal("report does not explain the compatibility-window gate")
			}
		})
	}
}
