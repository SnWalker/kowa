package logging

import (
	"io"
	"log/slog"
	"os"

	"github.com/SnWalker/kowa/internal/observability"
)

// New constructs the process logger.
func New() *slog.Logger {
	return NewWithWriter(os.Stdout)
}

// NewWithWriter constructs the process logger over an explicit writer. It exists
// so a test can read the exact bytes a record produces.
//
// Every record passes through the collection-time scrubber: a credential is
// masked where it is written, not hidden later in a user interface. The record
// shape is unchanged — keys stay where they were.
func NewWithWriter(writer io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{ReplaceAttr: observability.LogAttr}))
}
