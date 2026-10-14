package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"

func main() { os.Exit(realMain()) }

func realMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newRootCmd(version).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "envoyadmin:", err)
		return 1
	}
	return 0
}
