package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	PollInterval time.Duration `yaml:"poll_interval"`
	DBPath       string        `yaml:"db_path"`
	State        State         `yaml:"state"`
	Source       Source        `yaml:"source"`
	Resolver     Resolver      `yaml:"resolver"`
	Repo         Repo          `yaml:"repo"`
	Owner        Owner         `yaml:"owner"`
	Notify       Notify        `yaml:"notify"`
}

type State struct {
	GCS *GCS `yaml:"gcs"`
}

type GCS struct {
	Bucket string `yaml:"bucket"`
	Object string `yaml:"object"`
}

type Source struct {
	GCPLogging *GCPLogging `yaml:"gcp_logging"`
	File       string      `yaml:"file"`
}

type GCPLogging struct {
	// Applied in every environment.
	Filter       string        `yaml:"filter"`
	Overlap      time.Duration `yaml:"overlap"`
	Lookback     time.Duration `yaml:"lookback"`
	Environments []Environment `yaml:"environments"`
}

type Environment struct {
	Name    string `yaml:"name"`
	Project string `yaml:"project"`
}

// Uncaught exceptions land on stderr; Cloud Run's request logs go elsewhere
// and carry no stack trace.
const defaultFilter = `(log_id("run.googleapis.com/stdout") OR log_id("run.googleapis.com/stderr")) AND severity>=ERROR`

type Resolver struct {
	CloudRun *CloudRun         `yaml:"cloud_run"`
	Static   map[string]string `yaml:"static"`
}

type CloudRun struct {
	// Only services and jobs running this image are watched.
	Image string `yaml:"image"`
}

type Repo struct {
	Dir            string            `yaml:"dir"`
	IgnoreRevsFile string            `yaml:"ignore_revs_file"`
	FallbackRef    string            `yaml:"fallback_ref"`
	PathMap        map[string]string `yaml:"path_map"`
}

type Owner struct {
	Identities []string            `yaml:"identities"`
	Subsystems map[string][]string `yaml:"subsystems"`
}

type Notify struct {
	GChatWebhookEnv string `yaml:"gchat_webhook_env"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{PollInterval: 2 * time.Minute, DBPath: "pt.db"}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	for _, p := range []*string{&c.DBPath, &c.Repo.Dir} {
		*p = expandHome(*p)
	}
	if g := c.Source.GCPLogging; g != nil && g.Filter == "" {
		g.Filter = defaultFilter
	}
	return c, c.validate()
}

func expandHome(path string) string {
	rest, ok := strings.CutPrefix(path, "~/")
	if !ok {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, rest)
}

func validateEnvironments(envs []Environment) error {
	if len(envs) == 0 {
		return fmt.Errorf("config: source.gcp_logging needs at least one environment")
	}
	seen := map[string]bool{}
	for i, e := range envs {
		if e.Name == "" || e.Project == "" {
			return fmt.Errorf("config: environment %d needs name and project", i+1)
		}
		if seen[e.Name] {
			return fmt.Errorf("config: environment %q is listed twice", e.Name)
		}
		seen[e.Name] = true
	}
	return nil
}

func (c *Config) validate() error {
	if (c.Source.GCPLogging == nil) == (c.Source.File == "") {
		return fmt.Errorf("config: set exactly one of source.gcp_logging or source.file")
	}
	if g := c.Source.GCPLogging; g != nil {
		if err := validateEnvironments(g.Environments); err != nil {
			return err
		}
	}
	if (c.Resolver.CloudRun == nil) == (c.Resolver.Static == nil) {
		return fmt.Errorf("config: set exactly one of resolver.cloud_run or resolver.static")
	}
	if c.Repo.Dir == "" {
		return fmt.Errorf("config: repo.dir is required")
	}
	if len(c.Owner.Identities) == 0 {
		return fmt.Errorf("config: owner.identities is required")
	}
	return nil
}
