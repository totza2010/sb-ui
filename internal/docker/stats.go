package docker

import (
	"encoding/json"
	"strings"
)

// Stat is one container's live resource usage (from `docker stats`).
type Stat struct {
	Name    string `json:"name"`
	CPUPerc string `json:"cpu"`     // e.g. "3.21%"
	MemUL   string `json:"mem"`     // e.g. "1.2GiB / 15.6GiB"
	MemPerc string `json:"mem_pct"` // e.g. "7.69%"
	NetIO   string `json:"net"`     // e.g. "1.2MB / 3.4MB"
	BlockIO string `json:"block"`   // e.g. "0B / 12MB"
}

// Stats returns name → live usage for running containers (one `docker stats` call).
func Stats() map[string]Stat {
	rc, out := run("docker", "stats", "--no-stream", "--format", "{{json .}}")
	m := map[string]Stat{}
	if rc != 0 {
		return m
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row struct {
			Name, CPUPerc, MemUsage, MemPerc, NetIO, BlockIO string
		}
		if json.Unmarshal([]byte(line), &row) != nil {
			continue
		}
		m[row.Name] = Stat{
			Name: row.Name, CPUPerc: row.CPUPerc, MemUL: row.MemUsage,
			MemPerc: row.MemPerc, NetIO: row.NetIO, BlockIO: row.BlockIO,
		}
	}
	return m
}
