package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/oauth2/google"

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
	client, err := newGCPClient(ctx, cfg)
	if err != nil {
		return err
	}
	sources := newSources(cfg, client)
	p := &pipeline.Pipeline{
		Resolver:    newResolver(cfg, client),
		FallbackRef: cfg.Repo.FallbackRef,
		Matcher:     newMatcher(cfg),
		Store:       db,
		Notifier:    notifier,
	}

	if once {
		pollErr := poll(ctx, p, sources)
		return errors.Join(pollErr, state.Push(ctx, cfg.DBPath))
	}

	t := time.NewTicker(cfg.PollInterval)
	defer t.Stop()
	for {
		if err := poll(ctx, p, sources); err != nil {
			fmt.Fprintln(os.Stderr, "pt: poll:", err)
		}
		if err := state.Push(ctx, cfg.DBPath); err != nil {
			fmt.Fprintln(os.Stderr, "pt: push state:", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Read-only access with the user's own Application Default Credentials.
func newGCPClient(ctx context.Context, cfg *config.Config) (*http.Client, error) {
	if cfg.Source.GCPLogging == nil && cfg.Resolver.CloudRun == nil {
		return nil, nil
	}
	client, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/cloud-platform.read-only")
	if err != nil {
		return nil, fmt.Errorf("gcp credentials (run `gcloud auth application-default login`): %w", err)
	}
	return client, nil
}

type namedSource struct {
	name string
	src  pipeline.Source
}

// Environments are polled independently: one failing doesn't stop the rest.
func poll(ctx context.Context, p *pipeline.Pipeline, sources []namedSource) error {
	var errs []error
	for _, s := range sources {
		if err := p.RunOnce(ctx, s.name, s.src); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.name, err))
		}
	}
	return errors.Join(errs...)
}

func newSources(cfg *config.Config, client *http.Client) []namedSource {
	g := cfg.Source.GCPLogging
	if g == nil {
		return []namedSource{{name: "file", src: &file.Source{Path: cfg.Source.File}}}
	}
	sources := make([]namedSource, len(g.Environments))
	for i, e := range g.Environments {
		sources[i] = namedSource{name: e.Name, src: &gcplogging.Source{
			Environment: e.Name,
			Project:     e.Project,
			Filter:      g.Filter,
			Overlap:     g.Overlap,
			Lookback:    g.Lookback,
			Client:      client,
		}}
	}
	return sources
}

func newResolver(cfg *config.Config, client *http.Client) pipeline.Resolver {
	if cfg.Resolver.CloudRun != nil {
		return &cloudrun.Resolver{Client: client, Image: cfg.Resolver.CloudRun.Image}
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
