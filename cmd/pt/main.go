package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hhaidrr/personal-telemetry/internal/adapters/blame/gitcli"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/notify/gchat"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/notify/stdout"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/resolve/cloudrun"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/resolve/static"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/source/file"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/source/gcplogging"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/statefile/gcs"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/statefile/local"
	"github.com/hhaidrr/personal-telemetry/internal/config"
	"github.com/hhaidrr/personal-telemetry/internal/ownership"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
	"github.com/hhaidrr/personal-telemetry/internal/store"
)

type stateFile interface {
	Pull(ctx context.Context, dbPath string) error
	Push(ctx context.Context, dbPath string) error
}

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	once := flag.Bool("once", false, "run a single poll and exit (for scheduled jobs)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, *configPath, *once); err != nil {
		fmt.Fprintln(os.Stderr, "pt:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, configPath string, once bool) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	var state stateFile = local.StateFile{}
	if g := cfg.State.GCS; g != nil {
		state = &gcs.StateFile{Bucket: g.Bucket, Object: g.Object}
	}
	if err := state.Pull(ctx, cfg.DBPath); err != nil {
		return err
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	notifier, err := newNotifier(cfg)
	if err != nil {
		return err
	}
	src := newSource(cfg)
	p := &pipeline.Pipeline{
		Resolver: newResolver(cfg),
		Matcher:  newMatcher(cfg),
		Store:    db,
		Notifier: notifier,
	}

	if once {
		if err := p.RunOnce(ctx, src); err != nil {
			return err
		}
		return state.Push(ctx, cfg.DBPath)
	}

	t := time.NewTicker(cfg.PollInterval)
	defer t.Stop()
	for {
		if err := p.RunOnce(ctx, src); err != nil {
			fmt.Fprintln(os.Stderr, "pt: poll:", err)
		} else if err := state.Push(ctx, cfg.DBPath); err != nil {
			fmt.Fprintln(os.Stderr, "pt: push state:", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func newSource(cfg *config.Config) pipeline.Source {
	if g := cfg.Source.GCPLogging; g != nil {
		return &gcplogging.Source{Project: g.Project, Filter: g.Filter}
	}
	return &file.Source{Path: cfg.Source.File}
}

func newResolver(cfg *config.Config) pipeline.Resolver {
	if c := cfg.Resolver.CloudRun; c != nil {
		return &cloudrun.Resolver{Project: c.Project, Region: c.Region, Label: c.Label}
	}
	return static.Resolver(cfg.Resolver.Static)
}

func newMatcher(cfg *config.Config) *ownership.Matcher {
	ids := make(map[string]bool, len(cfg.Owner.Identities))
	for _, id := range cfg.Owner.Identities {
		ids[strings.ToLower(id)] = true
	}
	return &ownership.Matcher{
		Blamer:     &gitcli.Blamer{RepoDir: cfg.Repo.Dir, IgnoreRevsFile: cfg.Repo.IgnoreRevsFile},
		Identities: ids,
		Subsystems: cfg.Owner.Subsystems,
		PathMap:    cfg.Repo.PathMap,
	}
}

func newNotifier(cfg *config.Config) (pipeline.Notifier, error) {
	env := cfg.Notify.GChatWebhookEnv
	if env == "" {
		return stdout.Notifier{}, nil
	}
	webhook := os.Getenv(env)
	if webhook == "" {
		return nil, fmt.Errorf("notify: %s is not set", env)
	}
	return &gchat.Notifier{WebhookURL: webhook}, nil
}
