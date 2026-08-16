package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// runRoot executes the root command with the given args, capturing whatever
// it writes to stdout/stderr. Cobra writes --version output to stdout.
func runRoot(t *testing.T, args ...string) string {
	t.Helper()

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("executing %v: %v", args, err)
	}
	return out.String()
}

// TestVersionFlagReportsBuildInfo is the regression test for the release
// binaries reporting no version at all: main's -X ldflags targeted symbols
// that did not exist, and the root command never set Version, so
// "cortex --version" failed with "unknown flag: --version".
func TestVersionFlagReportsBuildInfo(t *testing.T) {
	SetVersionInfo("1.0.1", "abc1234", "2026-08-16T22:00:00Z")

	got := runRoot(t, "--version")

	for _, want := range []string{"1.0.1", "abc1234", "2026-08-16T22:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("--version output missing %q\ngot: %s", want, got)
		}
	}
	if !strings.Contains(got, "cortex") {
		t.Errorf("--version output should name the binary\ngot: %s", got)
	}
}

// TestVersionFlagDefaultsToDev covers a binary built without ldflags — a
// plain "go build ./cmd/cortex" or "go run". It must still answer
// --version rather than erroring.
func TestVersionFlagDefaultsToDev(t *testing.T) {
	SetVersionInfo("", "", "")

	got := runRoot(t, "--version")

	if !strings.Contains(got, "dev") {
		t.Errorf("--version with no build info should report dev\ngot: %s", got)
	}
}

// TestSetVersionInfoPartial covers a build that sets only some ldflags:
// the known values are reported and the unset ones fall back rather than
// rendering as empty fragments.
func TestSetVersionInfoPartial(t *testing.T) {
	SetVersionInfo("1.0.1", "", "")

	got := runRoot(t, "--version")

	if !strings.Contains(got, "1.0.1") {
		t.Errorf("--version output missing the version\ngot: %s", got)
	}
	if strings.Contains(got, "()") {
		t.Errorf("--version output has an empty build-info group\ngot: %s", got)
	}
}
