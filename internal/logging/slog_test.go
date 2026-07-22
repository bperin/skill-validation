package logging

import (
	"bytes"
	"log/slog"
	"testing"
)

func TestNew(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := New("development", slog.LevelInfo, buf)
	logger.Info("test")
	if buf.Len() == 0 {
		t.Error("expected log output")
	}
}
