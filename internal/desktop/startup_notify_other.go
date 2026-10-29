//go:build !windows

package desktop

func notifyStartupFailure(string) error { return nil }
