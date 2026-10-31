package cloudrun

import (
	"context"
	"errors"

	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

type Resolver struct {
	Project string
	Region  string
	Label   string
}

var _ pipeline.Resolver = (*Resolver)(nil)

func (r *Resolver) SHA(ctx context.Context, service, revision string) (string, error) {
	return "", errors.New("cloudrun: SHA not implemented")
}
