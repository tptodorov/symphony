package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/openai/symphony/go/internal/app"
	"github.com/openai/symphony/go/internal/observability"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	logsRoot := flag.String("logs-root", "", "logs directory")
	port := flag.Int("port", 0, "http port")
	flag.Parse()
	workflow := "WORKFLOW.md"
	if flag.NArg() > 0 {
		workflow = flag.Arg(0)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := observability.NewLogger(os.Stdout, slog.String("component", "symphony"))
	a, err := app.New(ctx, app.Options{WorkflowPath: workflow, LogsRoot: *logsRoot, Port: *port, Logger: log})
	if err != nil {
		return err
	}
	return a.Run(ctx)
}
