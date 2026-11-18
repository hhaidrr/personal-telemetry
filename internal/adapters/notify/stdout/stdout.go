package stdout

import (
	"context"
	"fmt"

	"github.com/hhaidrr/personal-telemetry/internal/adapters/notify/text"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

type Notifier struct{}

var _ pipeline.Notifier = Notifier{}

func (Notifier) Notify(ctx context.Context, a pipeline.Alert) error {
	_, err := fmt.Printf("%s\n\n", text.Format(a))
	return err
}
