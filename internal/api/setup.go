package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"sb-ui/internal/config"
	"sb-ui/internal/configfiles"
	"sb-ui/internal/executor"
)

func setupStatus(w http.ResponseWriter, _ *http.Request) {
	c := config.Get()
	mode := "local"
	host := any(nil)
	if c.IsRemote() {
		mode = "ssh"
		host = c.Host
	}
	authType := "key"
	if c.Password != "" {
		authType = "password"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": c.Configured, "mode": mode, "host": host,
		"user": c.User, "port": c.Port, "key": c.KeyPath, "auth_type": authType,
		"saltbox_configured": saltboxConfigured(),
	})
}

// saltboxConfigured reports whether the initial Saltbox setup has already run,
// i.e. accounts.yml has a real domain + username. Used to hide the Setup Wizard
// once the box is provisioned (it only matters on a fresh install).
func saltboxConfigured() bool {
	m, err := configfiles.Read("accounts")
	if err != nil {
		return false
	}
	u, ok := m["user"].(map[string]any)
	if !ok {
		return false
	}
	domain, _ := u["domain"].(string)
	name, _ := u["name"].(string)
	return strings.TrimSpace(domain) != "" && strings.TrimSpace(name) != ""
}

func setupTest(w http.ResponseWriter, req *http.Request) {
	var b struct {
		Host, User, Password, KeyPath, Passphrase, AuthType string
		Port                                                int
	}
	raw := map[string]any{}
	_ = json.NewDecoder(req.Body).Decode(&raw)
	b.Host, _ = raw["host"].(string)
	b.User, _ = raw["user"].(string)
	b.Password, _ = raw["password"].(string)
	b.KeyPath, _ = raw["key_path"].(string)
	b.Passphrase, _ = raw["passphrase"].(string)
	b.AuthType, _ = raw["auth_type"].(string)
	if p, ok := raw["port"].(float64); ok {
		b.Port = int(p)
	}
	if b.Host == "" {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "Host is required"})
		return
	}
	if b.Port == 0 {
		b.Port = 22
	}
	if b.User == "" {
		b.User = "seed"
	}
	if b.KeyPath == "" {
		b.KeyPath = "~/.ssh/id_rsa"
	}

	key, pass, phrase := "", "", ""
	if b.AuthType == "password" {
		pass = b.Password
	} else {
		key = config.ExpandPath(b.KeyPath)
		phrase = b.Passphrase
	}
	e := executor.NewSSH(b.Host, b.Port, b.User, key, phrase, pass)
	defer e.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	rc, out, err := e.Run(ctx, []string{"echo", "saltbox-ui-ok"}, "")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
		return
	}
	ms := float64(time.Since(start).Microseconds()) / 1000.0
	if rc == 0 && strings.Contains(out, "saltbox-ui-ok") {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "latency_ms": ms})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": "Unexpected response: " + out})
}

func setupSave(w http.ResponseWriter, req *http.Request) {
	raw := map[string]any{}
	_ = json.NewDecoder(req.Body).Decode(&raw)
	str := func(k string) string { s, _ := raw[k].(string); return s }
	mode := str("mode")
	authType := str("auth_type")

	var sb strings.Builder
	sb.WriteString("# Saltbox UI — managed by setup wizard\nSALTBOX_CONFIGURED=true\n")
	if mode == "local" {
		sb.WriteString("SALTBOX_HOST=\n")
	} else {
		port := "22"
		if p, ok := raw["port"].(float64); ok {
			port = strconv.Itoa(int(p))
		}
		user := str("user")
		if user == "" {
			user = "seed"
		}
		sb.WriteString("SALTBOX_HOST=" + str("host") + "\n")
		sb.WriteString("SALTBOX_PORT=" + port + "\n")
		sb.WriteString("SALTBOX_USER=" + user + "\n")
		if authType == "password" {
			sb.WriteString("SALTBOX_PASSWORD=" + str("password") + "\n")
		} else {
			key := str("key_path")
			if key == "" {
				key = "~/.ssh/id_rsa"
			}
			sb.WriteString("SALTBOX_KEY=" + key + "\n")
			if ph := str("passphrase"); ph != "" {
				sb.WriteString("SALTBOX_PASSPHRASE=" + ph + "\n")
			}
		}
	}

	if err := os.WriteFile(config.EnvPath(), []byte(sb.String()), 0o600); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
		return
	}

	// Reload config + hot-swap executor (no restart).
	cfg := config.Load()
	config.Set(cfg)
	if old := executor.Get(); old != nil {
		_ = old.Close()
	}
	executor.Set(executor.Make(cfg))
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "connect_warning": nil})
}
