package extproc

import (
	"log/slog"
	"os"
	"strings"
)

func getLogLevelFromEnv() slog.Level {
	levelStr := os.Getenv("LOG_LEVEL")
	switch strings.ToUpper(levelStr) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	case "INFO", "": // Default to INFO if not set or recognized
		return slog.LevelInfo
	default:
		// You might want to log a warning here before the logger is fully configured
		return slog.LevelInfo
	}
}
