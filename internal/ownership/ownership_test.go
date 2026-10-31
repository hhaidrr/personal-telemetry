package ownership

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/hhaidrr/personal-telemetry/internal/event"
)

func TestPathMapResolve(t *testing.T) {
	pm := PathMap{"/workspace/": "src/", "/workspace/vendor/": "third_party/"}
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"/workspace/payments/channels/x.py", "src/payments/channels/x.py", true},
		{"/workspace/vendor/lib.py", "third_party/lib.py", true},
		{"/usr/lib/python3.12/json/decoder.py", "", false},
		{"/workspace/.venv/lib/python3.12/site-packages/django/db.py", "", false},
	}
	for _, tt := range tests {
		got, ok := pm.Resolve(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Resolve(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		glob, path string
		want       bool
	}{
		{"src/payments/channels/**", "src/payments/channels/x.py", true},
		{"src/payments/channels/**", "src/payments/channels/a/b/x.py", true},
		{"src/payments/channels/**", "src/payments/channelsx/x.py", false},
		{"src/**/captures/*.py", "src/payments/domain/captures/c.py", true},
		{"src/**/captures/*.py", "src/captures/c.py", true},
		{"src/flows/create_batch_*/**", "src/flows/create_batch_debit/tests/t.py", true},
		{"src/payments/x.py", "src/payments/x.py", true},
		{"src/payments/x.py", "src/payments/y.py", false},
	}
	for _, tt := range tests {
		if got := globMatch(tt.glob, tt.path); got != tt.want {
			t.Errorf("globMatch(%q, %q) = %v; want %v", tt.glob, tt.path, got, tt.want)
		}
	}
}

type fakeBlamer map[string]string

func (f fakeBlamer) Author(_ context.Context, _, path string, line int) (string, error) {
	if a, ok := f[key(path, line)]; ok {
		return a, nil
	}
	return "teammate@example.com", nil
}

func key(path string, line int) string { return path + ":" + strconv.Itoa(line) }

func TestMatcher(t *testing.T) {
	frames := []event.Frame{
		{Path: "/workspace/.venv/lib/site-packages/adyen/client.py", Line: 10, Func: "post"},
		{Path: "/workspace/payments/repository.py", Line: 40, Func: "save"},
		{Path: "/workspace/payments/channels/router.py", Line: 7, Func: "route"},
	}
	subsystems := map[string][]string{"payment-channels": {"src/payments/channels/**"}}

	tests := []struct {
		name  string
		blame fakeBlamer
		want  Match
	}{
		{
			name:  "owner wrote the deepest in-repo line",
			blame: fakeBlamer{"src/payments/repository.py:40": "Me@Example.com"},
			want:  Match{Tier: Primary, Frame: event.Frame{Path: "src/payments/repository.py", Line: 40, Func: "save"}},
		},
		{
			name:  "owner wrote a call site further out",
			blame: fakeBlamer{"src/payments/channels/router.py:7": "me@example.com"},
			want: Match{Tier: Secondary, Subsystem: "payment-channels",
				Frame: event.Frame{Path: "src/payments/channels/router.py", Line: 7, Func: "route"}},
		},
		{
			name:  "nobody's line is the owner's, but a frame is in a sub-system",
			blame: fakeBlamer{},
			want: Match{Tier: Subsystem, Subsystem: "payment-channels",
				Frame: event.Frame{Path: "src/payments/channels/router.py", Line: 7, Func: "route"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Matcher{
				Blamer:     tt.blame,
				Identities: map[string]bool{"me@example.com": true},
				Subsystems: subsystems,
				PathMap:    PathMap{"/workspace/": "src/"},
			}
			got, err := m.Match(context.Background(), "abc123", frames)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestMatcherNoMatch(t *testing.T) {
	m := &Matcher{
		Blamer:     fakeBlamer{},
		Identities: map[string]bool{"me@example.com": true},
		PathMap:    PathMap{"/workspace/": "src/"},
	}
	got, err := m.Match(context.Background(), "abc123", []event.Frame{{Path: "/workspace/core/x.py", Line: 1}})
	if err != nil || got.Tier != None {
		t.Errorf("got %+v, %v; want no match", got, err)
	}
}

type errBlamer struct{}

func (errBlamer) Author(context.Context, string, string, int) (string, error) {
	return "", errors.New("no such path at commit")
}

func TestMatcherBlameError(t *testing.T) {
	m := &Matcher{Blamer: errBlamer{}, PathMap: PathMap{"/workspace/": "src/"}}
	if _, err := m.Match(context.Background(), "abc123", []event.Frame{{Path: "/workspace/x.py", Line: 1}}); err == nil {
		t.Error("want blame error to surface")
	}
}
