package logging

import (
	"log/slog"
	"os"
)

// New constructs the process logger.
func New() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, nil))
}
