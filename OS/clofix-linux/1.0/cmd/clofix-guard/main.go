package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/clofix/clofix-linux/internal/agent"
	"github.com/clofix/clofix-linux/internal/config"
)

func main() {
	cfgPath := flag.String("config", "/etc/clofix/guard/config.json", "config path")
	once := flag.Bool("once", false, "run a single full scan and exit")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(2)
	}
	a, err := agent.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent: %v\n", err)
		os.Exit(1)
	}
	if *once {
		rep, err := a.FullScan()
		if err != nil {
			fmt.Fprintf(os.Stderr, "scan: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("scan complete; score=%v data=%s\n", rep["score"], cfg.DataDir)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := a.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}
}
