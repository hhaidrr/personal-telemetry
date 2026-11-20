package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/oauth2/google"

	"github.com/hhaidrr/personal-telemetry/internal/adapters/blame/gitcli"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/notify/desktop"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/notify/ntfy"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/notify/route"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/notify/stdout"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/resolve/cloudrun"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/resolve/static"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/source/file"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/source/gcplogging"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/statefile/gcs"
	"github.com/hhaidrr/personal-telemetry/internal/adapters/statefile/local"
	"github.com/hhaidrr/personal-telemetry/internal/config"
	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/health"
	"github.com/hhaidrr/personal-telemetry/internal/ownership"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
	"github.com/hhaidrr/personal-telemetry/internal/store"
	"github.com/hhaidrr/personal-telemetry/internal/tui"
)

type stateFile interface {
	Pull(ctx context.Context, dbPath string) error
	Push(ctx context.Context, dbPath string) error
}

const usage = `usage: pt [command] [-config path]

commands:
  (none)       open the terminal interface
  run          poll the logs and send alerts (what the background service runs)
  test-notify  send a sample alert through every notification channel
`

func main() {
	cmd, args := "tui", os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("pt "+cmd, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage); fs.PrintDefaults() }
	configPath := fs.String("config", defaultConfigPath(), "path to config file")
	once := false
	if cmd == "run" {
		fs.BoolVar(&once, "once", false, "run a single poll and exit")
	}
	fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "tui":
		err = runTUI(ctx, *configPath)
	case "run":
		err = run(ctx, *configPath, once)
	case "test-notify":
		err = sendTestAlert(ctx, *configPath)
	default:
		fs.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pt:", err)
		os.Exit(1)
	}
}

func defaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(dir, "pt", "config.yaml")
}

func runTUI(ctx context.Context, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	var envs []string
	var message tui.MessageFunc
	if g := cfg.Source.GCPLogging; g != nil {
		for _, e := range g.Environments {
			envs = append(envs, e.Name)
		}
		client, clientErr := newGCPClient(ctx, cfg)
		message = func(ctx context.Context, project, eventID string) (string, error) {
			if clientErr != nil {
				return "", clientErr
			}
			return gcplogging.Message(ctx, client, project, eventID)
		}
	}
	return tui.Run(ctx, db, message, tui.Options{Environments: envs, Subsystems: cfg.Owner.Subsystems, Ignore: cfg.Owner.Ignore, Link: gcplogging.Link})
}

func run(ctx context.Context, configPath string, once bool) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	unlock, err := lock(cfg.DBPath)
	if err != nil {
		return err
	}
	defer unlock()

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
	monitor := &health.Monitor{}
	for {
		err := poll(ctx, p, sources)
		if err != nil {
			fmt.Fprintln(os.Stderr, "pt: poll:", err)
		}
		if a := monitor.Observe(err, time.Now()); a != nil && ctx.Err() == nil {
			if err := notifier.Notify(ctx, *a); err != nil {
				fmt.Fprintln(os.Stderr, "pt: notify about pt itself:", err)
			}
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

// A second pt on the same database would read the same logs and alert twice.
// The kernel drops the lock when the process exits, however it exits.
func lock(dbPath string) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(dbPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another pt is already running on %s (systemctl --user status pt)", dbPath)
		}
		return nil, fmt.Errorf("lock %s: %w", dbPath, err)
	}
	return func() { f.Close() }, nil
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
		Ignore:     cfg.Owner.Ignore,
		PathMap:    cfg.Repo.PathMap,
	}
}

func newNotifier(cfg *config.Config) (pipeline.Notifier, error) {
	routes := route.Notifier{{Name: "stdout", Notifier: stdout.Notifier{}}}
	if d := cfg.Notify.Desktop; d != nil {
		routes = append(routes, route.Route{Name: "desktop", Notifier: desktop.Notifier{}, When: route.When(d.When)})
	}
	if n := cfg.Notify.Ntfy; n != nil {
		topic := os.Getenv(n.TopicEnv)
		if topic == "" {
			return nil, fmt.Errorf("notify: %s is not set", n.TopicEnv)
		}
		routes = append(routes, route.Route{Name: "ntfy", Notifier: &ntfy.Notifier{Server: n.Server, Topic: topic}, When: route.When(n.When)})
	}
	return routes, nil
}

func sendTestAlert(ctx context.Context, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	n, err := newNotifier(cfg)
	if err != nil {
		return err
	}
	return n.Notify(ctx, pipeline.Alert{
		Occurrence: pipeline.Occurrence{
			ExcType:     "TestAlert",
			Environment: "production",
			Match: ownership.Match{
				Tier:  ownership.Primary,
				Frame: event.Frame{Path: "src/pt/test.py", Line: 1, Func: "test_notify"},
			},
			Revision: "pt-test",
			Link:     "https://console.cloud.google.com/logs",
		},
		State:   pipeline.FingerprintState{Count: 1, FirstSeen: time.Now()},
		Message: "This is a test alert from pt -test-notify.",
	})
}
