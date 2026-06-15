package docker

import (
	"encoding/json"
	"strings"
)

type ContainerInfo struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Status  string            `json:"status"`  // human, e.g. "Up 20 minutes"
	Running bool              `json:"running"` // from docker State == "running"
	Image   string            `json:"image"`
	Ports   map[string]string `json:"ports"`
}

// ListContainers returns all containers (docker ps -a).
func ListContainers() []ContainerInfo {
	rc, out := run("docker", "ps", "-a", "--format", "{{json .}}")
	res := []ContainerInfo{}
	if rc != 0 {
		return res
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row struct {
			ID, Names, Status, State, Image, Ports string
		}
		if json.Unmarshal([]byte(line), &row) != nil {
			continue
		}
		id := row.ID
		if len(id) > 12 {
			id = id[:12]
		}
		res = append(res, ContainerInfo{
			ID: id, Name: row.Names, Status: row.Status, Running: row.State == "running",
			Image: row.Image, Ports: parsePorts(row.Ports),
		})
	}
	return res
}

func parsePorts(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	for _, part := range strings.Split(s, ", ") {
		part = strings.TrimSpace(part)
		if i := strings.LastIndex(part, "->"); i >= 0 {
			hostSide, containerSide := part[:i], part[i+2:]
			hp := hostSide
			if j := strings.LastIndex(hostSide, ":"); j >= 0 {
				hp = hostSide[j+1:]
			}
			out[containerSide] = hp
		}
	}
	return out
}
