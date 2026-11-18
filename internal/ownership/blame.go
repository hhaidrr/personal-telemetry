package ownership

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/hhaidrr/personal-telemetry/internal/event"
)

// A frame is the owner's if it's in one of their subsystems, or if blame
// credits them with its line and it isn't in a path they've disowned.
type Matcher struct {
	Blamer     Blamer
	Identities map[string]bool
	Subsystems map[string][]string
	// Globs for code the owner touched but doesn't own; subsystems override it.
	Ignore  []string
	PathMap PathMap
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
		if Ignored(m.Ignore, m.Subsystems, f.Path) {
			continue
		}
		authors, err := m.Blamer.Authors(ctx, sha, f.Path, f.Line)
		if err != nil {
			return Match{}, fmt.Errorf("blame %s:%d at %s: %w", f.Path, f.Line, sha, err)
		}
		if slices.ContainsFunc(authors, func(a string) bool { return m.Identities[strings.ToLower(a)] }) {
			tier := Secondary
			if i == 0 {
				tier = Primary
			}
			return Match{Tier: tier, Frame: f, Subsystem: SubsystemFor(m.Subsystems, f.Path)}, nil
		}
	}

	for _, f := range inRepo {
		if s := SubsystemFor(m.Subsystems, f.Path); s != "" {
			return Match{Tier: Subsystem, Frame: f, Subsystem: s}, nil
		}
	}
	return Match{}, nil
}
