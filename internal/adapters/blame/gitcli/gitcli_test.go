package gitcli

import (
	"reflect"
	"testing"
)

const porcelain = `1111111111111111111111111111111111111111 1 1 2
author Me
author-mail <Me@Example.com>
author-time 1700000000
author-tz +0000
committer Me
committer-mail <me@example.com>
committer-time 1700000000
committer-tz +0000
summary add router
filename payments/channels/router.py
	def route(payment):
1111111111111111111111111111111111111111 2 2
	    return pick(payment)
2222222222222222222222222222222222222222 5 3 1
author Teammate
author-mail <teammate@example.com>
author-time 1700000500
author-tz +0000
committer Teammate
committer-mail <teammate@example.com>
committer-time 1700000500
committer-tz +0000
summary tweak
previous 1111111111111111111111111111111111111111 payments/channels/router.py
filename payments/channels/router.py
	author-mail <not-a-header@example.com>
1111111111111111111111111111111111111111 3 4 1
	# 1111111111111111111111111111111111111111 1 1
`

func TestParsePorcelain(t *testing.T) {
	got, err := parsePorcelain([]byte(porcelain))
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]string{
		1: "me@example.com",
		2: "me@example.com",
		3: "teammate@example.com",
		4: "me@example.com",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestParsePorcelainEmpty(t *testing.T) {
	got, err := parsePorcelain(nil)
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v; want empty", got, err)
	}
}
