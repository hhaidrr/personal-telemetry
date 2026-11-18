package ownership

import (
	"context"

	"github.com/hhaidrr/personal-telemetry/internal/event"
)

type Tier int

const (
	None Tier = iota
	Subsystem
	Secondary
	Primary
)

func (t Tier) String() string {
	switch t {
	case Subsystem:
		return "subsystem"
	case Secondary:
		return "secondary"
	case Primary:
		return "primary"
	}
	return "none"
}

type Match struct {
	Tier      Tier
	Frame     event.Frame
	Subsystem string
}

// Authors returns everyone a line can be credited to. Different blame
// strategies disagree on moved or copied code, and the line counts as the
// owner's if any of them says so.
type Blamer interface {
	Authors(ctx context.Context, sha, path string, line int) ([]string, error)
}
