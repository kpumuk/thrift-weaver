// Package testutil provides shared helpers for repository tests.
package testutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	envThriftOracleBin           = "THRIFT_ORACLE_BIN"
	envThriftOracleVersionPrefix = "THRIFT_ORACLE_VERSION_PREFIX"
	envThriftOracleRequired      = "THRIFT_ORACLE_REQUIRED"
)

// ThriftOracle runs the official `thrift` compiler as a syntax compatibility oracle.
type ThriftOracle struct {
	Bin           string
	VersionPrefix string
	Required      bool
}

// ThriftOracleFromEnv builds oracle configuration from environment variables.
func ThriftOracleFromEnv() ThriftOracle {
	bin := strings.TrimSpace(os.Getenv(envThriftOracleBin))
	if bin == "" {
		bin = "thrift"
	}
	required := strings.TrimSpace(os.Getenv(envThriftOracleRequired))
	return ThriftOracle{
		Bin:           bin,
		VersionPrefix: strings.TrimSpace(os.Getenv(envThriftOracleVersionPrefix)),
		Required:      required == "1" || strings.EqualFold(required, "true"),
	}
}

// RequireThriftOracle returns a configured oracle or skips the test when unavailable.
func RequireThriftOracle(t testing.TB) ThriftOracle {
	t.Helper()

	oracle := ThriftOracleFromEnv()
	if err := oracle.CheckAvailability(context.Background()); err != nil {
		if oracle.Required {
			t.Fatalf("thrift oracle unavailable: %v", err)
		}
		t.Skipf("skipping thrift oracle compatibility test: %v", err)
	}
	return oracle
}

// CheckAvailability verifies the binary exists and matches the configured version prefix (if any).
func (o ThriftOracle) CheckAvailability(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if _, err := exec.LookPath(o.Bin); err != nil {
		return fmt.Errorf("look up %q: %w", o.Bin, err)
	}

	if o.VersionPrefix == "" {
		return nil
	}

	version, err := o.Version(ctx)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(version, o.VersionPrefix) {
		return fmt.Errorf("oracle version %q does not match required prefix %q", version, o.VersionPrefix)
	}
	return nil
}

// Version returns `thrift -version` output.
func (o ThriftOracle) Version(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	//nolint:gosec // Test helper intentionally executes a configured local thrift binary.
	cmd := exec.CommandContext(ctx, o.Bin, "-version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run %s -version: %w (%s)", o.Bin, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// ValidateFile runs the Thrift compiler against path and returns an error on parse/generation failure.
func (o ThriftOracle) ValidateFile(ctx context.Context, path string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("empty path")
	}

	outDir, err := os.MkdirTemp("", "thrift-oracle-out-*")
	if err != nil {
		return fmt.Errorf("mkdir temp: %w", err)
	}
	defer func() { _ = os.RemoveAll(outDir) }()

	// `--gen cpp` is used as a parsing oracle; generated outputs are discarded.
	//nolint:gosec // Test helper intentionally executes a configured local thrift binary on a temporary fixture path.
	cmd := exec.CommandContext(ctx, o.Bin, "--gen", "cpp", "-out", outDir, filepath.Clean(path))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("thrift oracle validation failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
