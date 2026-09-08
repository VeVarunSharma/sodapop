package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/VeVarunSharma/sodapop/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()
	if err := app.Run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "sodapop:", err)
		os.Exit(1)
	}
}
