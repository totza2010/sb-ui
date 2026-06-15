// Package configfiles reads/writes the Saltbox YAML config files
// (accounts / settings / adv_settings / backup_config). Port of config_parser.py.
//
// NOTE: writing round-trips through a map, so YAML comments are not preserved
// (parity gap vs ruamel — acceptable per the migration plan; raw file editing
// via the fs editor preserves comments).
package configfiles

import (
	"context"
	"fmt"
	"time"

	"github.com/goccy/go-yaml"

	"sb-ui/internal/config"
	"sb-ui/internal/executor"
)

var Allowed = map[string]bool{
	"accounts": true, "settings": true, "adv_settings": true, "backup_config": true,
}

// Read returns the parsed config (falling back to the .default if not present).
func Read(name string) (map[string]any, error) {
	c := config.Get()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e := executor.Get()

	path := c.ConfigPath(name)
	if ok, _ := e.FileExists(ctx, path); !ok {
		def := c.ConfigDefaultPath(name)
		if ok, _ := e.FileExists(ctx, def); ok {
			path = def
		} else {
			return map[string]any{}, nil
		}
	}
	content, err := e.ReadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if yaml.Unmarshal([]byte(content), &m) != nil || m == nil {
		return map[string]any{}, nil
	}
	return m, nil
}

func Write(name string, data map[string]any) error {
	if !Allowed[name] {
		return fmt.Errorf("config file not allowed: %s", name)
	}
	out, err := yaml.Marshal(data)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return executor.Get().WriteFile(ctx, config.Get().ConfigPath(name), string(out))
}

// ApplyTag maps a config file to the ansible tag that applies it.
func ApplyTag(name string) string {
	switch name {
	case "accounts":
		return "user"
	case "settings", "adv_settings":
		return "settings"
	}
	return ""
}
