package parse

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/hhaidrr/personal-telemetry/internal/event"
)

func parseOK(t *testing.T, message string) ([]event.Frame, string) {
	t.Helper()
	frames, excType, err := Python(message)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return frames, excType
}

func TestPythonKeepsInnermostFrameFirstAndSkipsLocals(t *testing.T) {
	message := `Internal Server Error: /stripe-webhook
Traceback (most recent call last):
  File "/usr/local/lib/python3.12/site-packages/django/core/handlers/exception.py", line 55, in inner
    response = get_response(request)
               ^^^^^^^^^^^^^^^^^^^^^
    exc = RuntimeError("You called this URL via POST")
    request = <WSGIRequest: POST '/stripe-webhook'>
  File "/usr/local/lib/python3.12/site-packages/django/middleware/common.py", line 87, in get_full_path_with_slash
    raise RuntimeError(
    new_path = '/stripe-webhook/'
    self = <CommonMiddleware get_response=convert_exception_to_response.<locals>.inner>
RuntimeError: You called this URL via POST, but the URL doesn't end in a slash`

	frames, excType := parseOK(t, message)

	want := []event.Frame{
		{Path: "/usr/local/lib/python3.12/site-packages/django/middleware/common.py", Line: 87, Func: "get_full_path_with_slash"},
		{Path: "/usr/local/lib/python3.12/site-packages/django/core/handlers/exception.py", Line: 55, Func: "inner"},
	}
	if !reflect.DeepEqual(frames, want) {
		t.Errorf("frames = %+v, want %+v", frames, want)
	}
	if excType != "RuntimeError" {
		t.Errorf("excType = %q, want RuntimeError", excType)
	}
}

func TestPythonIgnoresLocalsThatLookLikeFrames(t *testing.T) {
	message := `Failed to collect or send analytics.
Traceback (most recent call last):
  File "/workspace/endpoints/graphql/analytics.py", line 73, in record_analytics
    if context["field_name"] == "mixpanelEvent":
       ~~~~~~~^^^^^^^^^^^^^^
    context = {}
    File "/workspace/fake.py", line 1, in fake
    e = KeyError('field_name')
KeyError: 'field_name'`

	frames, excType := parseOK(t, message)

	want := []event.Frame{{Path: "/workspace/endpoints/graphql/analytics.py", Line: 73, Func: "record_analytics"}}
	if !reflect.DeepEqual(frames, want) {
		t.Errorf("frames = %+v, want %+v", frames, want)
	}
	if excType != "KeyError" {
		t.Errorf("excType = %q, want KeyError", excType)
	}
}

func TestPythonSurvivesFormattingChanges(t *testing.T) {
	canonical := "Payout failed\n" +
		"Traceback (most recent call last):\n" +
		"  File \"/workspace/payouts/batch.py\", line 12, in run\n" +
		"    send(item)\n" +
		"    item = 'p_1'\n" +
		"  [Previous line repeated 2 more times]\n" +
		"  File \"/workspace/payouts/client.py\", line 40, in send\n" +
		"    raise ValueError(\"bad\")\n" +
		"ValueError: bad\n"

	variants := map[string]string{
		"canonical":      canonical,
		"tabs":           strings.ReplaceAll(canonical, "  ", "\t"),
		"wider indent":   strings.ReplaceAll(canonical, "  ", "    "),
		"frames flush":   strings.ReplaceAll(strings.ReplaceAll(canonical, "    ", "\t"), "  ", ""),
		"crlf":           strings.ReplaceAll(canonical, "\n", "\r\n"),
		"trailing space": strings.ReplaceAll(canonical, "\n", "  \n"),
		"ansi colors": strings.NewReplacer(
			"File ", "File \x1b[35m", "ValueError", "\x1b[1;35mValueError\x1b[0m",
		).Replace(canonical),
	}
	want := []event.Frame{
		{Path: "/workspace/payouts/client.py", Line: 40, Func: "send"},
		{Path: "/workspace/payouts/batch.py", Line: 12, Func: "run"},
	}

	for name, message := range variants {
		t.Run(name, func(t *testing.T) {
			frames, excType := parseOK(t, message)
			if !reflect.DeepEqual(frames, want) {
				t.Errorf("frames = %+v, want %+v", frames, want)
			}
			if excType != "ValueError" {
				t.Errorf("excType = %q, want ValueError", excType)
			}
		})
	}
}

func TestPythonPutsLoggedExceptionBeforeItsCause(t *testing.T) {
	message := `Payout failed
Traceback (most recent call last):
  File "/workspace/payouts/client.py", line 40, in send
    raise KeyError("missing")
    api_token = '[redacted]'
KeyError: 'missing'

The above exception was the direct cause of the following exception:

Traceback (most recent call last):
  File "/workspace/payouts/batch.py", line 12, in run
    send(item)
  File "/workspace/payouts/batch.py", line 30, in send
    raise PayoutError("send failed") from e
payouts.errors.PayoutError: send failed
  File "/workspace/not/a/frame.py", line 1, in message_continues`

	frames, excType := parseOK(t, message)

	want := []event.Frame{
		{Path: "/workspace/payouts/batch.py", Line: 30, Func: "send"},
		{Path: "/workspace/payouts/batch.py", Line: 12, Func: "run"},
		{Path: "/workspace/payouts/client.py", Line: 40, Func: "send"},
	}
	if !reflect.DeepEqual(frames, want) {
		t.Errorf("frames = %+v, want %+v", frames, want)
	}
	if excType != "payouts.errors.PayoutError" {
		t.Errorf("excType = %q, want payouts.errors.PayoutError", excType)
	}
}

func TestPythonExceptionWithoutMessage(t *testing.T) {
	message := `Unhandled exception in APIRootView
Traceback (most recent call last):
  File "/usr/local/lib/python3.12/site-packages/rest_framework/negotiation.py", line 88, in filter_renderers
    raise Http404
    format = 'env'
django.http.response.Http404`

	_, excType := parseOK(t, message)

	if excType != "django.http.response.Http404" {
		t.Errorf("excType = %q, want django.http.response.Http404", excType)
	}
}

func TestPythonSyntaxErrorFrameHasNoFunction(t *testing.T) {
	message := `Traceback (most recent call last):
  File "/workspace/manage.py", line 22, in main
    import_module(name)
  File "/workspace/payouts/broken.py", line 3
    def f(:
          ^
SyntaxError: invalid syntax`

	frames, excType := parseOK(t, message)

	if frames[0] != (event.Frame{Path: "/workspace/payouts/broken.py", Line: 3}) {
		t.Errorf("frames[0] = %+v", frames[0])
	}
	if excType != "SyntaxError" {
		t.Errorf("excType = %q, want SyntaxError", excType)
	}
}

func TestPythonMessageWithoutTracebackIsNotAnError(t *testing.T) {
	for _, message := range []string{
		"Failed to authenticate retool webhook request: <Request: post '/retool-operation/'>.",
		"[locals omitted: entry was 300 KB, limit 240 KB] charge failed\n[ValueError dropped: entry too large]",
		"",
	} {
		frames, excType, err := Python(message)
		if frames != nil || excType != "" || err != nil {
			t.Errorf("Python(%q) = %+v, %q, %v, want nothing", message, frames, excType, err)
		}
	}
}

func TestPythonReportsUnreadableTracebacksWithWhatItCouldParse(t *testing.T) {
	cases := map[string]struct {
		message    string
		wantFrames int
	}{
		"truncated before exception line": {
			message:    "Traceback (most recent call last):\n  File \"/workspace/a.py\", line 1, in f\n    f()\n",
			wantFrames: 1,
		},
		"unknown frame format": {
			message: "Traceback (most recent call last):\n  at f (/workspace/a.py:1)\nValueError: bad\n",
		},
		"exception group": {
			message: "  + Exception Group Traceback (most recent call last):\n" +
				"  |   File \"/workspace/a.py\", line 1, in f\n" +
				"  | ExceptionGroup: failed (1 sub-exception)\n",
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			frames, _, err := Python(c.message)
			if !errors.Is(err, ErrUnrecognized) {
				t.Errorf("err = %v, want ErrUnrecognized", err)
			}
			if len(frames) != c.wantFrames {
				t.Errorf("got %d frames, want %d", len(frames), c.wantFrames)
			}
		})
	}
}
