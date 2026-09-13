package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/m-tsuru/ats-radio-server/internal/config"
	"github.com/m-tsuru/ats-radio-server/internal/daemon"
)

func main() {
	configPath := flag.String("config", config.DefaultPath, "path to daemon JSON configuration")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}
	service, err := daemon.New(cfg)
	if err != nil {
		log.Fatalf("initialize daemon: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := service.Serve(ctx); err != nil {
		log.Fatalf("radiod: %v", err)
	}
}
