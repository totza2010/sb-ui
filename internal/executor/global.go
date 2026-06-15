package executor

import (
	"sync"

	"sb-ui/internal/config"
)

var (
	mu      sync.RWMutex
	current Executor = LocalExecutor{}
)

// Set installs the active executor (called at startup / after setup).
func Set(e Executor) {
	mu.Lock()
	defer mu.Unlock()
	current = e
}

// Get returns the active executor.
func Get() Executor {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Make builds the right executor from config: SSH when a host is set, else local.
func Make(c *config.Config) Executor {
	if !c.IsRemote() {
		return LocalExecutor{}
	}
	return NewSSH(c.Host, c.Port, c.User, c.ExpandKeyPath(), c.Passphrase, c.Password)
}
