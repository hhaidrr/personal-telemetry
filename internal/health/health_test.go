package health

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAlertsAfterRepeatedFailuresThenHourlyThenOnRecovery(t *testing.T) {
	m := &Monitor{}
	t0 := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	fail := errors.New(`develop: fetch: gcplogging: Post "https://logging.googleapis.com/v2/entries:list": oauth2: "invalid_grant" "reauth related error (invalid_rapt)"`)

	var alerts []string
	for i := range 40 {
		if a := m.Observe(fail, t0.Add(time.Duration(i)*2*time.Minute)); a != nil {
			alerts = append(alerts, a.System+": "+a.Message)
		}
	}
	// 40 polls two minutes apart: the 3rd (4 min in) and the one an hour after it.
	if len(alerts) != 2 {
		t.Fatalf("got %d alerts, want 2:\n%s", len(alerts), strings.Join(alerts, "\n"))
	}
	if !strings.Contains(alerts[0], "pt can't read logs: Google credentials expired") ||
		!strings.Contains(alerts[0], "gcloud auth application-default login") {
		t.Errorf("auth failure alert = %s", alerts[0])
	}

	a := m.Observe(nil, t0.Add(80*time.Minute))
	if a == nil || a.System != "pt is reading logs again" || a.Message != "after 1h20m0s of failures" {
		t.Errorf("recovery alert = %+v", a)
	}
	if a := m.Observe(nil, t0.Add(82*time.Minute)); a != nil {
		t.Errorf("second success alerted again: %+v", a)
	}
}

func TestBriefFailuresStayQuiet(t *testing.T) {
	m := &Monitor{}
	t0 := time.Now()
	for i, err := range []error{errors.New("503"), errors.New("503"), nil, errors.New("503"), errors.New("503"), nil} {
		if a := m.Observe(err, t0.Add(time.Duration(i)*time.Minute)); a != nil {
			t.Fatalf("poll %d alerted: %+v", i, a)
		}
	}
}

func TestOtherFailuresShowTheError(t *testing.T) {
	m := &Monitor{FailAfter: 1}
	a := m.Observe(errors.New("production: fetch: gcplogging: 403 Forbidden: PERMISSION_DENIED\n{details}"), time.Now())
	if a == nil || strings.Contains(a.Message, "gcloud auth") || !strings.Contains(a.Message, "403 Forbidden: PERMISSION_DENIED") || strings.Contains(a.Message, "{details}") {
		t.Errorf("alert = %+v", a)
	}
}
