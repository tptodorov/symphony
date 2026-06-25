package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/tptodorov/symphony/go/internal/app"
	"github.com/tptodorov/symphony/go/internal/observability"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type cliOptions struct {
	LogsRoot     string
	Port         int
	PortSet      bool
	WorkDir      string
	WorkflowPath string
}

func run() error {
	opts, err := parseArgs(os.Args[1:], os.Args[0], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if opts.WorkDir != "" {
		if err := os.Chdir(opts.WorkDir); err != nil {
			return fmt.Errorf("change workdir: %w", err)
		}
	}
	if err := setRuntimeEnv(); err != nil {
		return err
	}
	logWriter := io.Writer(os.Stdout)
	if opts.LogsRoot != "" {
		if err := os.MkdirAll(opts.LogsRoot, 0o700); err != nil {
			return fmt.Errorf("create logs root: %w", err)
		}
		f, err := os.OpenFile(filepath.Join(opts.LogsRoot, "symphony.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		defer f.Close()
		logWriter = io.MultiWriter(os.Stdout, f)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := observability.NewLogger(logWriter, slog.String("component", "symphony"))
	a, err := app.New(ctx, app.Options{WorkflowPath: opts.WorkflowPath, LogsRoot: opts.LogsRoot, Port: opts.Port, PortSet: opts.PortSet, Logger: log})
	if err != nil {
		return err
	}
	return a.Run(ctx)
}

func setRuntimeEnv() error {
	workdir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve workdir: %w", err)
	}
	if err := os.Setenv("SYMPHONY_WORKDIR", workdir); err != nil {
		return fmt.Errorf("set SYMPHONY_WORKDIR: %w", err)
	}
	return nil
}

func parseArgs(args []string, name string, output io.Writer) (cliOptions, error) {
	opts := cliOptions{WorkflowPath: "WORKFLOW.md"}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(output)
	fs.StringVar(&opts.LogsRoot, "logs-root", "", "logs directory")
	fs.IntVar(&opts.Port, "port", 0, "http port")
	fs.StringVar(&opts.WorkDir, "workdir", "", "working directory for Symphony")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage of %s [flags] [path-to-WORKFLOW.md]:\n", name)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			opts.PortSet = true
		}
	})
	if fs.NArg() > 1 {
		return cliOptions{}, fmt.Errorf("expected at most one workflow path, got %d", fs.NArg())
	}
	if fs.NArg() == 1 {
		opts.WorkflowPath = fs.Arg(0)
	}
	return opts, nil
}
