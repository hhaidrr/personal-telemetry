package config

import "testing"

func TestValidateWhenCatchesTypos(t *testing.T) {
	c := &Config{Source: Source{GCPLogging: &GCPLogging{Environments: []Environment{{Name: "production", Project: "p"}}}}}
	if err := c.validateWhen("ntfy", When{Environments: []string{"production"}, Tiers: []string{"primary", "unparsed"}}); err != nil {
		t.Errorf("valid rule rejected: %v", err)
	}
	if err := c.validateWhen("ntfy", When{Environments: []string{"prod"}}); err == nil {
		t.Error("want error for an unknown environment")
	}
	if err := c.validateWhen("ntfy", When{Tiers: []string{"primray"}}); err == nil {
		t.Error("want error for an unknown tier")
	}
}

func TestValidateEnvironments(t *testing.T) {
	ok := Environment{Name: "develop", Project: "divdot-develop"}
	cases := map[string]struct {
		envs    []Environment
		wantErr bool
	}{
		"valid":           {envs: []Environment{ok, {Name: "production", Project: "divdot-production"}}},
		"none":            {wantErr: true},
		"missing project": {envs: []Environment{{Name: "develop"}}, wantErr: true},
		"duplicate name":  {envs: []Environment{ok, ok}, wantErr: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateEnvironments(c.envs); (err != nil) != c.wantErr {
				t.Errorf("err = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}
