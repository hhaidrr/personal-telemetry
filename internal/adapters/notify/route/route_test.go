package route

import (
	"context"
	"errors"
	"testing"

	"github.com/hhaidrr/personal-telemetry/internal/ownership"
	"github.com/hhaidrr/personal-telemetry/internal/pipeline"
)

type recorder struct {
	got int
	err error
}

func (r *recorder) Notify(context.Context, pipeline.Alert) error {
	r.got++
	return r.err
}

func alert(env string, tier ownership.Tier) pipeline.Alert {
	return pipeline.Alert{Occurrence: pipeline.Occurrence{Environment: env, Match: ownership.Match{Tier: tier}}}
}

func TestRoutesByEnvironmentAndTier(t *testing.T) {
	desktop, phone := &recorder{}, &recorder{}
	n := Notifier{
		{Name: "desktop", Notifier: desktop},
		{Name: "phone", Notifier: phone, When: When{Environments: []string{"production"}, Tiers: []string{"primary", "subsystem"}}},
	}
	unparsed := alert("production", ownership.None)
	unparsed.ParseError = "no frames"
	for _, a := range []pipeline.Alert{
		{System: "pt can't read logs"},
		alert("production", ownership.Primary),
		alert("production", ownership.Secondary),
		alert("develop", ownership.Primary),
		unparsed,
	} {
		if err := n.Notify(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	if desktop.got != 5 || phone.got != 2 {
		t.Errorf("desktop got %d, phone got %d; want 5 and 2 (pt's own alert reaches both)", desktop.got, phone.got)
	}
}

func TestFailsOnlyWhenNoMatchingChannelDelivered(t *testing.T) {
	down := &recorder{err: errors.New("offline")}
	if err := (Notifier{{Name: "a", Notifier: down}, {Name: "b", Notifier: &recorder{}}}).Notify(context.Background(), alert("p", ownership.Primary)); err != nil {
		t.Errorf("one channel delivered, got %v", err)
	}
	if err := (Notifier{{Name: "a", Notifier: down}}).Notify(context.Background(), alert("p", ownership.Primary)); err == nil {
		t.Error("want an error when nothing delivered")
	}
}
