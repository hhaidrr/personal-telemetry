package config

import (
	"fmt"
	"os"
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
	Project string `yaml:"project"`
	Filter  string `yaml:"filter"`
}

type Resolver struct {
	CloudRun *CloudRun         `yaml:"cloud_run"`
	Static   map[string]string `yaml:"static"`
}

type CloudRun struct {
	Project string `yaml:"project"`
	Region  string `yaml:"region"`
	Label   string `yaml:"label"`
}

type Repo struct {
	Dir            string            `yaml:"dir"`
	IgnoreRevsFile string            `yaml:"ignore_revs_file"`
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
	return c, c.validate()
}

func (c *Config) validate() error {
	if (c.Source.GCPLogging == nil) == (c.Source.File == "") {
		return fmt.Errorf("config: set exactly one of source.gcp_logging or source.file")
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
