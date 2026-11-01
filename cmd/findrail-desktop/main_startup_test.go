package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestDesktopCommandStartupUsesNotifierButVersionAndPDFWorkerDoNot(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{name: "fatal desktop argument", args: []string{"--bad-option"}, want: true},
		{name: "version", args: []string{"version"}},
		{name: "pdf extraction child", args: []string{"__extract-pdf"}},
		{name: "CLI machine command", args: []string{"index", "notes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			calls := 0
			notify := func(message string) error {
				calls++
				if len(message) > 512 {
					t.Fatalf("notice exceeds bound: %d", len(message))
				}
				return nil
			}
			code := run(context.Background(), tc.args, &out, &stderr, true, notify)
			if tc.want {
				if code == 0 || calls != 1 || !strings.Contains(stderr.String(), "findrail-desktop:") {
					t.Fatalf("startup result: code=%d notifier calls=%d stderr=%q", code, calls, stderr.String())
				}
			} else if calls != 0 {
				t.Fatalf("machine command displayed %d dialogs", calls)
			}
		})
	}
}
