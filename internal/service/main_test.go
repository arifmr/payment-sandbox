package service

import (
	"io"
	"log/slog"
	"os"
	"testing"
)

// TestMain silences the package default logger. The reuse-detection path logs a security
// warning by design, and tests deliberately trigger it — without this, expected warnings
// bury the actual test output.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}
