package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/neverknowerdev/paylessforai/server/runtime"
)

func main() {
	if err := runtime.Run(context.Background(), os.Args[1:]); err != nil {
		slog.Error("paylessforai-server stopped", "error", err)
		os.Exit(1)
	}
}
