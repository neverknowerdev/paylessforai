package main

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/neverknowerdev/paylessforai/app/runtime"
	"github.com/neverknowerdev/paylessforai/internal/buildinfo"
	"github.com/neverknowerdev/paylessforai/internal/updater"
)

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-version" {
			println(buildinfo.Version)
			return
		}
	}
	internal := false
	preflight := false
	args := make([]string, 0, len(os.Args)-1)
	for _, arg := range os.Args[1:] {
		if arg == "--internal-serve" {
			internal = true
			continue
		}
		if arg == "--internal-preflight" {
			preflight = true
			continue
		}
		args = append(args, arg)
	}
	if preflight {
		if err := runtime.Preflight(args); err != nil {
			slog.Error("paylessforai preflight failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if !internal {
		if err := updater.RunSupervisor(context.Background(), args); err != nil {
			slog.Error("paylessforai supervisor stopped", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := runtime.Run(context.Background(), args); err != nil {
		if errors.Is(err, runtime.ErrUpdateRequested) {
			os.Exit(updater.UpdateRequestedExitCode)
		}
		slog.Error("paylessforai-app stopped", "error", err)
		os.Exit(1)
	}
}
