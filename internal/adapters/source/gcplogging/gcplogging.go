package gcplogging

import (
	"context"
	"errors"

	"github.com/hhaidrr/personal-telemetry/internal/event"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

type Source struct {
	Project string
	Filter  string
}

var _ pipeline.Source = (*Source)(nil)

func (s *Source) Fetch(ctx context.Context, cursor pipeline.Cursor) ([]event.Event, pipeline.Cursor, error) {
	return nil, cursor, errors.New("gcplogging: Fetch not implemented")
}
