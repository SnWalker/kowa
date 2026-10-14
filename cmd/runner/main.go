package main

import (
	"fmt"
	"os"

	"github.com/SnWalker/kowa/internal/runner"
)

func main() {
	exit := runner.NewExitCode()
	app := runner.New(exit)
	if err := app.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "runner configuration error:", err)
		os.Exit(1)
	}
	app.Run()
	if err := app.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "runner stopped:", err)
		os.Exit(1)
	}
	if code := exit.Code(); code != 0 {
		os.Exit(code)
	}
}
