// Package config loads sb-ui settings from the same .env the Python backend
// uses (SALTBOX_* keys), so both can share one config file.
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Configured bool

	Host       string
	Port       int
	User       string
	KeyPath    string
	Passphrase string
	Password   string

	SaltboxRepo   string
	SandboxRepo   string
	SaltboxModRepo string
	AnsibleBin    string
}

func (c *Config) IsRemote() bool { return c.Host != "" }

// Active config singleton (set at startup), so packages can read settings
// without threading *Config everywhere.
var active = defaults()

func Set(c *Config) { active = c }
func Get() *Config  { return active }

func (c *Config) SaltboxPlaybook() string   { return c.SaltboxRepo + "/saltbox.yml" }
func (c *Config) SandboxPlaybook() string   { return c.SandboxRepo + "/sandbox.yml" }
func (c *Config) SaltboxModPlaybook() string { return c.SaltboxModRepo + "/saltbox_mod.yml" }
func (c *Config) CacheFile() string         { return c.SaltboxRepo + "/cache.json" }
func (c *Config) ConfigPath(name string) string        { return c.SaltboxRepo + "/" + name + ".yml" }
func (c *Config) ConfigDefaultPath(name string) string { return c.SaltboxRepo + "/defaults/" + name + ".yml.default" }

// ExpandKeyPath resolves a leading ~ to the user's home directory.
func (c *Config) ExpandKeyPath() string { return ExpandPath(c.KeyPath) }

// ExpandPath resolves a leading ~ to the user's home directory.
func ExpandPath(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// EnvPath returns the .env location (existing one if found, else cwd/.env).
func EnvPath() string {
	if p := findEnv(); p != "" {
		return p
	}
	dir, _ := os.Getwd()
	return filepath.Join(dir, ".env")
}

func defaults() *Config {
	return &Config{
		Port:           22,
		User:           "seed",
		KeyPath:        "~/.ssh/id_rsa",
		SaltboxRepo:    "/srv/git/saltbox",
		SandboxRepo:    "/opt/sandbox",
		SaltboxModRepo: "/opt/saltbox_mod",
		AnsibleBin:     "/usr/local/bin/ansible-playbook",
	}
}

// Load reads .env (searching cwd then parent dirs) and overlays env vars.
func Load() *Config {
	c := defaults()
	for k, v := range readEnvFile(findEnv()) {
		apply(c, k, v)
	}
	// Real environment variables override the file.
	for _, k := range []string{
		"SALTBOX_CONFIGURED", "SALTBOX_HOST", "SALTBOX_PORT", "SALTBOX_USER",
		"SALTBOX_KEY", "SALTBOX_PASSPHRASE", "SALTBOX_PASSWORD",
		"SALTBOX_REPO", "SANDBOX_REPO", "SALTBOX_MOD_REPO", "ANSIBLE_BIN",
	} {
		if v, ok := os.LookupEnv(k); ok {
			apply(c, k, v)
		}
	}
	return c
}

func apply(c *Config, key, val string) {
	switch strings.ToUpper(key) {
	case "SALTBOX_CONFIGURED":
		c.Configured = val == "true" || val == "1" || val == "yes"
	case "SALTBOX_HOST":
		c.Host = val
	case "SALTBOX_PORT":
		if n, err := strconv.Atoi(val); err == nil {
			c.Port = n
		}
	case "SALTBOX_USER":
		c.User = val
	case "SALTBOX_KEY":
		c.KeyPath = val
	case "SALTBOX_PASSPHRASE":
		c.Passphrase = val
	case "SALTBOX_PASSWORD":
		c.Password = val
	case "SALTBOX_REPO":
		c.SaltboxRepo = val
	case "SANDBOX_REPO":
		c.SandboxRepo = val
	case "SALTBOX_MOD_REPO":
		c.SaltboxModRepo = val
	case "ANSIBLE_BIN":
		c.AnsibleBin = val
	}
}

func findEnv() string {
	dir, _ := os.Getwd()
	for i := 0; i < 4; i++ {
		p := filepath.Join(dir, ".env")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func readEnvFile(path string) map[string]string {
	out := map[string]string{}
	if path == "" {
		return out
	}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}
