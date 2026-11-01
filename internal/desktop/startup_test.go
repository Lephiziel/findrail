package desktop

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type exitError struct{ code int }

func (e exitError) Error() string { return "synthetic startup failure: private SQL detail" }
func (e exitError) ExitCode() int { return e.code }

func TestStartupFailureNotifiesOnceWithBoundedSafeTextAndPreservesExit(t *testing.T) {
	err := exitError{code: 7}
	var stderr bytes.Buffer
	var notices []string
	code := ReportStartupFailure(err, true, &stderr, func(message string) error {
		notices = append(notices, message)
		return errors.New("native UI unavailable")
	})
	if code != 7 {
		t.Fatalf("exit code = %d, want original 7", code)
	}
	if len(notices) != 1 {
		t.Fatalf("notification count = %d, want 1", len(notices))
	}
	if len(notices[0]) > 512 || !strings.Contains(notices[0], "Findrail could not start") ||
		strings.Contains(notices[0], "private SQL detail") || strings.Contains(notices[0], "synthetic startup failure") {
		t.Fatalf("unsafe or unbounded notice: %q", notices[0])
	}
	if !strings.Contains(stderr.String(), err.Error()) {
		t.Fatalf("stderr lost original error: %q", stderr.String())
	}
}

func TestStartupDialogPolicyExcludesMachineInvocationsAndAutomation(t *testing.T) {
	lookup := func(values map[string]string) func(string) (string, bool) {
		return func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	}
	for _, args := range [][]string{{}, {"--data-dir", "isolated"}, {"oops"}} {
		if !StartupDialogAllowed(args, true, false, lookup(nil)) {
			t.Errorf("interactive desktop args %q were suppressed", args)
		}
	}
	for _, args := range [][]string{{"version"}, {"__extract-pdf"}, {"index", "notes"}, {"doctor"}, {"--help"}} {
		if StartupDialogAllowed(args, true, false, lookup(nil)) {
			t.Errorf("machine invocation %q would show a modal dialog", args)
		}
	}
	for _, env := range []map[string]string{{"CI": "true"}, {"GITHUB_ACTIONS": "true"}, {"FINDRAIL_NO_GUI_ERRORS": "1"}} {
		if StartupDialogAllowed(nil, true, false, lookup(env)) {
			t.Errorf("controlled invocation with %v would show a dialog", env)
		}
	}
	if StartupDialogAllowed(nil, true, true, lookup(nil)) || StartupDialogAllowed(nil, false, false, lookup(nil)) {
		t.Fatal("console or unsupported platform should keep stderr-only behavior")
	}
}

func TestStartupFailureDoesNotNotifyOnCancellationOrSuccess(t *testing.T) {
	var stderr bytes.Buffer
	calls := 0
	notify := func(string) error { calls++; return nil }
	if code := ReportStartupFailure(nil, true, &stderr, notify); code != 0 {
		t.Fatalf("successful invocation exit = %d", code)
	}
	if code := ReportStartupFailure(context.Canceled, true, &stderr, notify); code != 1 {
		t.Fatalf("cancellation exit = %d", code)
	}
	if calls != 0 {
		t.Fatalf("success/cancellation showed %d notifications", calls)
	}
}
