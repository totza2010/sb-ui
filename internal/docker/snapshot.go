package docker

import (
	"sync"
	"time"
)

// snapTTL keeps a single `docker ps -a` result shared across the many consumers
// that fire during one dashboard load (container list, app list, status bar,
// image lookups). Short enough that start/stop shows up quickly, and we also
// invalidate explicitly on container actions.
const snapTTL = 3 * time.Second

var (
	snapMu   sync.Mutex
	snapData []ContainerInfo
	snapOK   bool
	snapAt   time.Time
)

// snapshot returns the cached container list + daemon reachability, refreshing
// at most once per snapTTL.
func snapshot() ([]ContainerInfo, bool) {
	snapMu.Lock()
	defer snapMu.Unlock()
	if !snapAt.IsZero() && time.Since(snapAt) < snapTTL {
		return snapData, snapOK
	}
	snapData, snapOK = fetchContainers()
	snapAt = time.Now()
	return snapData, snapOK
}

// invalidate drops the cache so the next read reflects a just-applied change.
func invalidate() {
	snapMu.Lock()
	snapAt = time.Time{}
	snapMu.Unlock()
}

// ListContainers returns all containers (docker ps -a), cached.
func ListContainers() []ContainerInfo {
	c, _ := snapshot()
	return c
}

// Reachable reports whether the docker daemon responded on the last snapshot.
func Reachable() bool {
	_, ok := snapshot()
	return ok
}

// RunningCount returns the number of running containers.
func RunningCount() int {
	c, _ := snapshot()
	n := 0
	for _, ci := range c {
		if ci.Running {
			n++
		}
	}
	return n
}

// RunningNames returns the set of running container names.
func RunningNames() map[string]bool {
	c, _ := snapshot()
	set := map[string]bool{}
	for _, ci := range c {
		if ci.Running {
			set[ci.Name] = true
		}
	}
	return set
}

// ContainerImages maps running container name → image string.
func ContainerImages() map[string]string {
	c, _ := snapshot()
	m := map[string]string{}
	for _, ci := range c {
		if ci.Running {
			m[ci.Name] = ci.Image
		}
	}
	return m
}
