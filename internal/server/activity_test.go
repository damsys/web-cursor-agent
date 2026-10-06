package server

import (
	"testing"
)

func TestClassifyTitle(t *testing.T) {
	cases := []struct {
		title string
		want  AgentActivity
	}{
		{"Working… | my-session", AgentActivityBusy},
		{"Waiting for you | my-session", AgentActivityWaiting},
		{"Waiting for confirmation | my-session", AgentActivityWaiting},
		{"Ready | my-session", AgentActivityIdle},
		{"Cursor Agent", AgentActivityUnknown},
		{"", AgentActivityUnknown},
	}
	for _, tc := range cases {
		if got := ClassifyTitle(tc.title); got != tc.want {
			t.Fatalf("ClassifyTitle(%q) = %v, want %v", tc.title, got, tc.want)
		}
	}
}

func TestSessionNameFromTitle(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		{"Cursor Title Display - ✅ Ready", "Cursor Title Display"},
		{"Cursor Title Display - 📂 Loading conversation", "Cursor Title Display"},
		{"Fix login - ⏳ Working…", "Fix login"},
		{"Fix login - Waiting for you", "Fix login"},
		{"Name - with dash - ✅ Ready", "Name - with dash"},
		{"Cursor Agent - ✅ Ready", ""},
		{"Ready | my-session", "my-session"},
		{"Working… | Fix login", "Fix login"},
		{"Ready", ""},
		{"Cursor Agent", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := SessionNameFromTitle(tc.title); got != tc.want {
			t.Fatalf("SessionNameFromTitle(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}

func TestActivityName(t *testing.T) {
	cases := []struct {
		activity AgentActivity
		want     string
	}{
		{AgentActivityUnknown, "unknown"},
		{AgentActivityIdle, "idle"},
		{AgentActivityBusy, "busy"},
		{AgentActivityWaiting, "waiting"},
	}
	for _, tc := range cases {
		if got := ActivityName(tc.activity); got != tc.want {
			t.Fatalf("ActivityName(%v) = %q, want %q", tc.activity, got, tc.want)
		}
	}
}

func TestOSCScannerReadsTitle(t *testing.T) {
	var titles []string
	scanner := &oscScanner{onTitle: func(title string) {
		titles = append(titles, title)
	}}
	scanner.Feed([]byte("hello\x1b]0;Working… | demo\x07world"))
	scanner.Feed([]byte("\x1b]2;Ready | demo\x1b\\"))
	if len(titles) != 2 || titles[0] != "Working… | demo" || titles[1] != "Ready | demo" {
		t.Fatalf("titles = %#v", titles)
	}
}

func TestOSCScannerHandlesSplitChunks(t *testing.T) {
	var titles []string
	scanner := &oscScanner{onTitle: func(title string) {
		titles = append(titles, title)
	}}
	scanner.Feed([]byte("\x1b]0;Rea"))
	scanner.Feed([]byte("dy | x\x07"))
	if len(titles) != 1 || titles[0] != "Ready | x" {
		t.Fatalf("titles = %#v", titles)
	}
}
