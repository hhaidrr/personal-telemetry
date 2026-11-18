package gchat

import "testing"

func TestThreadedURL(t *testing.T) {
	got := threadedURL("https://chat.googleapis.com/v1/spaces/S/messages?key=k&token=t", "ValueError@src/x.py:f")
	want := "https://chat.googleapis.com/v1/spaces/S/messages?key=k&token=t" +
		"&threadKey=ValueError%40src%2Fx.py%3Af&messageReplyOption=REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
