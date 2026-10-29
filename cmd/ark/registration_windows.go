//go:build windows

package main

// CRC: crc-Registration.md | Seq: seq-install.md#1.13, seq-install.md#8.8 | R3680, R3681, R3695
//
// The registry side of the registration. Both entries are under HKCU, so
// neither needs elevation.

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	// envSubkey holds the user's environment; Path is the value ark joins. R3680
	envSubkey    = `Environment`
	envPathValue = "Path"
	// settingChangeTimeoutMs bounds the broadcast, so a hung window cannot
	// stall setup. R3680
	settingChangeTimeoutMs = 5000
	hwndBroadcast          = 0xffff
	wmSettingChange        = 0x001A
	smtoAbortIfHung        = 0x0002
)

var procSendMessageTimeoutW = windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")

// Register puts arkDir on the user Path and writes every value of the
// installed-apps entry, reporting whether Path changed. R3680, R3681
func Register(arkDir, version string) (bool, error) {
	changed, err := editUserPath(func(path string) (string, bool) { return pathWithEntry(path, arkDir) })
	if err != nil {
		return false, err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, arpSubkey, registry.ALL_ACCESS)
	if err != nil {
		return changed, err
	}
	defer key.Close()
	strs, dwords := arpValues(arkDir, version)
	for name, v := range strs {
		if err := key.SetStringValue(name, v); err != nil {
			return changed, err
		}
	}
	for name, v := range dwords {
		if err := key.SetDWordValue(name, v); err != nil {
			return changed, err
		}
	}
	return changed, nil
}

// Unregister removes arkDir from the user Path and deletes the installed-apps
// entry; either one already gone is not an error. R3695
func Unregister(arkDir string) error {
	if _, err := editUserPath(func(path string) (string, bool) { return pathWithoutEntry(path, arkDir) }); err != nil {
		return err
	}
	if err := registry.DeleteKey(registry.CURRENT_USER, arpSubkey); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// editUserPath rewrites the user Path through edit, keeping the value's type
// (REG_EXPAND_SZ unless it was a plain REG_SZ), and announces a change. R3680
func editUserPath(edit func(string) (string, bool)) (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, envSubkey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false, err
	}
	defer key.Close()
	path, valType, err := key.GetStringValue(envPathValue)
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return false, err
	}
	newPath, changed := edit(path)
	if !changed {
		return false, nil
	}
	if valType == registry.SZ {
		err = key.SetStringValue(envPathValue, newPath)
	} else {
		err = key.SetExpandStringValue(envPathValue, newPath)
	}
	if err != nil {
		return false, err
	}
	announceEnvironmentChange()
	return true, nil
}

// announceEnvironmentChange tells running programs (Explorer, so the shells it
// starts) that the user environment changed. Best-effort. R3680
func announceEnvironmentChange() {
	param, err := syscall.UTF16PtrFromString(envSubkey)
	if err != nil {
		return
	}
	var result uintptr
	_, _, _ = procSendMessageTimeoutW.Call(hwndBroadcast, wmSettingChange, 0,
		uintptr(unsafe.Pointer(param)), smtoAbortIfHung, settingChangeTimeoutMs, uintptr(unsafe.Pointer(&result)))
}
