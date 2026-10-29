//go:build !windows

package desktop

func StartupDialogPlatformAvailable() bool { return false }

func HasInteractiveConsole() bool { return true }

func NotifyNativeStartupFailure(string) error { return nil }
