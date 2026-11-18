package static

import (
	"context"
	"fmt"

	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

type Resolver map[string]string

var _ pipeline.Resolver = Resolver(nil)

func (r Resolver) SHA(ctx context.Context, e event.Event) (string, error) {
	if sha, ok := r[e.Revision]; ok {
		return sha, nil
	}
	if sha, ok := r["*"]; ok {
		return sha, nil
	}
	return "", fmt.Errorf("static: no SHA for revision %q", e.Revision)
}
