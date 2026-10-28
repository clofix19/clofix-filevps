// clofix-worker is one binary: worker agent + clofix-fx engine + scheduler logic.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/clofix/filevps/worker/internal/agent"
	"github.com/clofix/filevps/worker/internal/config"
	"github.com/clofix/filevps/worker/internal/engine/lifecycle"
	"github.com/clofix/filevps/worker/internal/engine/namespaces"
	"github.com/clofix/filevps/worker/internal/hostcheck"
	"github.com/clofix/filevps/worker/internal/swap"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage: clofix-worker <command>
  run                 start the worker daemon (agent + fx engine + scheduler admission)
  enroll              enroll with a controller: --controller URL --ca ca.pem (token read from a secure prompt)
  check               validate host kernel features
  status              list local instances
  version`)
	os.Exit(2)
}

func main() {
	cmd := "run"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "fx-init": // internal: executed inside the new namespaces by the engine
		if err := namespaces.RunInit(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "fx-init:", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println("clofix-worker", agent.Version)
	case "check":
		bad := 0
		for _, r := range hostcheck.Run() {
			mark := "ok  "
			if !r.OK {
				mark = "FAIL"
				if r.Required {
					bad++
				}
			}
			fmt.Printf("[%s] %s %s\n", mark, r.Name, r.Detail)
		}
		if bad > 0 {
			os.Exit(1)
		}
	case "enroll":
		fs := flag.NewFlagSet("enroll", flag.ExitOnError)
		url := fs.String("controller", "", "controller worker-API URL, e.g. https://controller:8443")
		ca := fs.String("ca", "", "path to the controller CA certificate")
		fs.Parse(os.Args[2:])
		if *url == "" || *ca == "" {
			fs.Usage()
			os.Exit(2)
		}
		tok, err := agent.ReadSecret("Enrollment token: ")
		if err != nil {
			log.Fatal(err)
		}
		cfg, _ := config.Load()
		cfg, err = agent.Enroll(*url, *ca, tok, cfg)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("enrolled as", cfg.WorkerID)
	case "status":
		cfg, _ := config.Load()
		eng := lifecycle.New(cfg)
		eng.Load()
		for _, s := range eng.Statuses() {
			fmt.Printf("%-24s %-12s pid=%d %s\n", s.ID, s.State, s.Pid, s.Error)
		}
	case "run":
		run()
	default:
		usage()
	}
}

func run() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	if cfg.ZRAMMB > 0 {
		if err := swap.EnsureZRAM(cfg.ZRAMMB); err != nil {
			log.Printf("WARNING: configured ZRAM unavailable: %v", err)
		}
	}
	if bad := hostcheck.Failed(hostcheck.Run()); len(bad) > 0 && os.Getenv("CLOFIX_SKIP_HOSTCHECK") != "1" {
		for _, b := range bad {
			log.Printf("host check failed: %s %s", b.Name, b.Detail)
		}
		log.Fatal("host does not meet requirements (CLOFIX_SKIP_HOSTCHECK=1 to override for development)")
	}
	eng := lifecycle.New(cfg)
	if err := eng.Load(); err != nil {
		log.Printf("engine load: %v", err)
	}
	go eng.Reconcile()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var ag *agent.Agent
	for {
		cfg, _ = config.Load() // pick up enrollment done while we wait
		if ag, err = agent.New(cfg, eng); err == nil {
			break
		}
		log.Printf("waiting for enrollment: %v", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
	log.Printf("clofix-worker %s running as %s", agent.Version, cfg.WorkerID)
	ag.Run(ctx) // instances are intentionally left running on worker shutdown
	log.Println("clofix-worker stopped")
}
