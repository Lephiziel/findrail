//go:build windows

package desktop

import (
	"errors"
	"syscall"
	"unsafe"
)

const (
	messageBoxOK            = 0x00000000
	messageBoxIconError     = 0x00000010
	messageBoxSetForeground = 0x00010000
)

var messageBoxW = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")

func notifyStartupFailure(message string) error {
	text, err := syscall.UTF16PtrFromString(message)
	if err != nil {
		return err
	}
	title, err := syscall.UTF16PtrFromString("Findrail")
	if err != nil {
		return err
	}
	result, _, callErr := messageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(text)),
		uintptr(unsafe.Pointer(title)),
		messageBoxOK|messageBoxIconError|messageBoxSetForeground,
	)
	if result == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return callErr
		}
		return errors.New("Windows could not display the Findrail startup message")
	}
	return nil
}
