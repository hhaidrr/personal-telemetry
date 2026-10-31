package stdout

import (
	"context"
	"fmt"

	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

type Notifier struct{}

var _ pipeline.Notifier = Notifier{}

func (Notifier) Notify(ctx context.Context, a pipeline.Alert) error {
	o := a.Occurrence
	_, err := fmt.Printf("[%s] %s %s:%d (%s) seen %d times\n",
		o.Match.Tier, a.Message, o.Match.Frame.Path, o.Match.Frame.Line, o.Match.Frame.Func, a.State.Count)
	return err
}
