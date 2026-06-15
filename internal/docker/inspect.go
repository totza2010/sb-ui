package docker

import "encoding/json"

type InspectMount struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Type        string `json:"type"`
	RW          bool   `json:"rw"`
}

// Inspect is the curated subset of `docker inspect` shown in the detail drawer.
type Inspect struct {
	Created  string         `json:"created"`
	Restart  string         `json:"restart"`
	Env      []string       `json:"env"`
	Networks []string       `json:"networks"`
	Mounts   []InspectMount `json:"mounts"`
}

// InspectContainer returns curated inspect data, or nil if not found.
func InspectContainer(name string) *Inspect {
	rc, out := run("docker", "inspect", name)
	if rc != 0 {
		return nil
	}
	var arr []struct {
		Created    string
		HostConfig struct{ RestartPolicy struct{ Name string } }
		Config     struct{ Env []string }
		Mounts     []struct {
			Source, Destination, Type string
			RW                        bool
		}
		NetworkSettings struct{ Networks map[string]json.RawMessage }
	}
	if json.Unmarshal([]byte(out), &arr) != nil || len(arr) == 0 {
		return nil
	}
	c := arr[0]
	ins := &Inspect{Created: c.Created, Restart: c.HostConfig.RestartPolicy.Name, Env: c.Config.Env}
	for n := range c.NetworkSettings.Networks {
		ins.Networks = append(ins.Networks, n)
	}
	for _, m := range c.Mounts {
		ins.Mounts = append(ins.Mounts, InspectMount{m.Source, m.Destination, m.Type, m.RW})
	}
	return ins
}
