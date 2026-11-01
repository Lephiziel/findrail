//go:build windows

package desktop

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	messageBoxOK      = 0x00000000
	messageBoxIconErr = 0x00000010
	messageBoxTask    = 0x00002000
)

var (
	kernel32Startup = syscall.NewLazyDLL("kernel32.dll")
	getConsole      = kernel32Startup.NewProc("GetConsoleWindow")
	user32Startup   = windows.NewLazySystemDLL("user32.dll")
	messageBox      = user32Startup.NewProc("MessageBoxW")
)

func StartupDialogPlatformAvailable() bool { return true }

func HasInteractiveConsole() bool {
	hwnd, _, _ := getConsole.Call()
	return hwnd != 0
}

func NotifyNativeStartupFailure(message string) error {
	if len(message) > 512 {
		return errors.New("startup notice exceeds limit")
	}
	text, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return err
	}
	title, err := windows.UTF16PtrFromString("Findrail")
	if err != nil {
		return err
	}
	result, _, callErr := messageBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), messageBoxOK|messageBoxIconErr|messageBoxTask)
	if result == 0 {
		if callErr != syscall.Errno(0) {
			return callErr
		}
		return errors.New("Windows could not display the startup notice")
	}
	return nil
}
