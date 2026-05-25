package obs

import (
	"log/slog"
	"os"
)

// NewLogger creates a JSON-format slog.Logger writing to stdout at the given level.
// Accepted level strings: "debug", "info", "warn", "error". Any unrecognised value
// defaults to "info".
func NewLogger(level string) *slog.Logger {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})
	return slog.New(h)
}
