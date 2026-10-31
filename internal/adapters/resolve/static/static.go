package static

import (
	"context"
	"fmt"

	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

type Resolver map[string]string

var _ pipeline.Resolver = Resolver(nil)

func (r Resolver) SHA(ctx context.Context, service, revision string) (string, error) {
	if sha, ok := r[revision]; ok {
		return sha, nil
	}
	if sha, ok := r["*"]; ok {
		return sha, nil
	}
	return "", fmt.Errorf("static: no SHA for revision %q", revision)
}
