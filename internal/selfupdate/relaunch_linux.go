//go:build linux

package selfupdate

import (
	"os"
	"syscall"
)

// relaunch replaces the current process image with the freshly-swapped binary,
// keeping the same args + environment. Go marks its listening sockets
// close-on-exec, so the new process can rebind the same address.
func relaunch(self string) error {
	return syscall.Exec(self, os.Args, os.Environ())
}
