//go:build !linux

package selfupdate

import "errors"

// relaunch is unsupported off Linux (syscall.Exec is unix-only). The Run guard
// already rejects non-Linux before reaching here, so this is just a stub.
func relaunch(string) error { return errors.New("relaunch unsupported on this platform") }
