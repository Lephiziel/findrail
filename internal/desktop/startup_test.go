package desktop

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReportStartupFailureNotifiesOnceWithBoundedSafeMessage(t *testing.T) {
	failure := errors.New("open index: SQL details, token=synthetic-secret, /private/source.txt")
	var stderr bytes.Buffer
	var messages []string
	got := ReportStartupFailure([]string{"--invalid-task39"}, failure, &stderr, func(message string) error {
		messages = append(messages, message)
		return nil
	}, false)

	if got != failure {
		t.Fatalf("reported error = %v, want original error %v", got, failure)
	}
	if len(messages) != 1 {
		t.Fatalf("notification calls = %d, want one", len(messages))
	}
	message := messages[0]
	if len(message) > maxStartupFailureMessage {
		t.Fatalf("notification is %d bytes, limit is %d", len(message), maxStartupFailureMessage)
	}
	for _, want := range []string{"Findrail could not start", "selected data directory", "run findrail desktop"} {
		if !strings.Contains(message, want) {
			t.Errorf("notification %q does not contain %q", message, want)
		}
	}
	for _, unwanted := range []string{"SQL details", "synthetic-secret", "/private/source.txt"} {
		if strings.Contains(message, unwanted) {
			t.Errorf("notification leaked %q: %q", unwanted, message)
		}
	}
	if got := stderr.String(); !strings.Contains(got, failure.Error()) {
		t.Fatalf("stderr lost original diagnostic: %q", got)
	}
}

func TestReportStartupFailureIgnoresNotifierFailure(t *testing.T) {
	failure := errors.New("synthetic startup failure")
	var stderr bytes.Buffer
	calls := 0
	got := ReportStartupFailure(nil, failure, &stderr, func(string) error {
		calls++
		return errors.New("native dialog unavailable")
	}, false)

	if got != failure {
		t.Fatalf("reported error = %v, want original error %v", got, failure)
	}
	if calls != 1 {
		t.Fatalf("notification calls = %d, want one", calls)
	}
	if !strings.Contains(stderr.String(), failure.Error()) {
		t.Fatalf("stderr fallback missing original error: %q", stderr.String())
	}
}

func TestStartupFailureNotificationSkipsMachineAndNonfatalInvocations(t *testing.T) {
	failure := errors.New("synthetic failure")
	cases := []struct {
		name    string
		args    []string
		failure error
		machine bool
	}{
		{name: "version", args: []string{"version"}, failure: failure},
		{name: "PDF worker", args: []string{"__extract-pdf"}, failure: failure},
		{name: "help", args: []string{"--help"}, failure: failure},
		{name: "controlled machine invocation", args: []string{"--invalid-task39"}, failure: failure, machine: true},
		{name: "cancellation", failure: context.Canceled},
		{name: "deadline", failure: context.DeadlineExceeded},
		{name: "second-instance success", failure: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			calls := 0
			got := ReportStartupFailure(tc.args, tc.failure, &stderr, func(string) error {
				calls++
				return nil
			}, tc.machine)
			if got != tc.failure {
				t.Fatalf("reported error = %v, want %v", got, tc.failure)
			}
			if calls != 0 {
				t.Fatalf("notification calls = %d, want zero", calls)
			}
			if tc.failure == nil && stderr.Len() != 0 {
				t.Fatalf("successful operation wrote diagnostic: %q", stderr.String())
			}
		})
	}
}
