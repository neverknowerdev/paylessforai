package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/neverknowerdev/paylessforai/app/runtime"
)

func main() {
	if err := runtime.Run(context.Background(), os.Args[1:]); err != nil {
		slog.Error("paylessforai-app stopped", "error", err)
		os.Exit(1)
	}
}
