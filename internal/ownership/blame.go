package ownership

import (
	"context"
	"fmt"
	"strings"

	"github.com/hhaidrr/personal-telemetry/internal/event"
)

type Matcher struct {
	Blamer     Blamer
	Identities map[string]bool
	Subsystems map[string][]string
	PathMap    PathMap
}

func (m *Matcher) Match(ctx context.Context, sha string, frames []event.Frame) (Match, error) {
	var inRepo []event.Frame
	for _, f := range frames {
		if p, ok := m.PathMap.Resolve(f.Path); ok {
			f.Path = p
			inRepo = append(inRepo, f)
		}
	}

	for i, f := range inRepo {
		author, err := m.Blamer.Author(ctx, sha, f.Path, f.Line)
		if err != nil {
			return Match{}, fmt.Errorf("blame %s:%d at %s: %w", f.Path, f.Line, sha, err)
		}
		if m.Identities[strings.ToLower(author)] {
			tier := Secondary
			if i == 0 {
				tier = Primary
			}
			return Match{Tier: tier, Frame: f, Subsystem: subsystemFor(m.Subsystems, f.Path)}, nil
		}
	}

	for _, f := range inRepo {
		if s := subsystemFor(m.Subsystems, f.Path); s != "" {
			return Match{Tier: Subsystem, Frame: f, Subsystem: s}, nil
		}
	}
	return Match{}, nil
}
