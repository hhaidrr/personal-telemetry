// Package route sends each alert to the channels whose rules it matches.
package route

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

// Empty lists match everything. Parse-failure alerts have the tier "unparsed".
// Alerts about pt itself go everywhere: a rule mustn't hide that pt is down.
type When struct {
	Environments []string `yaml:"environments"`
	Tiers        []string `yaml:"tiers"`
}

type Route struct {
	Name     string
	Notifier pipeline.Notifier
	When     When
}

type Notifier []Route

var _ pipeline.Notifier = Notifier(nil)

// Delivered once any matching channel took it: failing the whole alert would
// make the pipeline retry it, repeating it on the channels that worked.
func (n Notifier) Notify(ctx context.Context, a pipeline.Alert) error {
	var errs []error
	delivered := false
	for _, r := range n {
		if !r.When.matches(a) {
			continue
		}
		if err := r.Notifier.Notify(ctx, a); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.Name, err))
			continue
		}
		delivered = true
	}
	if delivered && len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "pt: notify:", errors.Join(errs...))
		return nil
	}
	return errors.Join(errs...)
}

func (w When) matches(a pipeline.Alert) bool {
	if a.System != "" {
		return true
	}
	tier := a.Occurrence.Match.Tier.String()
	if a.ParseError != "" {
		tier = "unparsed"
	}
	return (len(w.Environments) == 0 || slices.Contains(w.Environments, a.Occurrence.Environment)) &&
		(len(w.Tiers) == 0 || slices.Contains(w.Tiers, tier))
}
