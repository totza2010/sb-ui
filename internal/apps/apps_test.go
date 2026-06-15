package apps

import (
	"sort"
	"testing"

	"sb-ui/internal/inventory"
)

func TestClassify(t *testing.T) {
	st := state{
		containers: map[string]bool{"plex": true},
		active:     map[string]bool{"rclone_gdrive": true, "rclone_gdrive_refresh": true},
		known:      map[string]string{},
		binaries:   map[string]bool{"rclone": true},
	}
	if ok, kind, _, _ := st.classify("plex"); !ok || kind != "container" {
		t.Fatalf("plex: ok=%v kind=%s", ok, kind)
	}
	// rclone has active rclone_* services → service; unit prefers the non-refresh one.
	if ok, kind, _, unit := st.classify("rclone"); !ok || kind != "service" || unit == nil || *unit != "rclone_gdrive" {
		t.Fatalf("rclone: ok=%v kind=%s unit=%v", ok, kind, unit)
	}
	if ok, kind, _, _ := st.classify("borg"); ok || kind != "unknown" {
		t.Fatalf("borg should be not-installed unknown")
	}
}

func TestInstanceNames(t *testing.T) {
	inv := map[string]any{"sonarr_instances": []any{"sonarrhd", "sonarruhd"}}
	got := instanceNames(inv, "sonarr")
	if len(got) != 2 || got[0] != "sonarrhd" || got[1] != "sonarruhd" {
		t.Fatalf("instances=%v", got)
	}
	if d := instanceNames(map[string]any{}, "plex"); len(d) != 1 || d[0] != "plex" {
		t.Fatalf("default=%v", d)
	}
}

func TestDependsNames(t *testing.T) {
	cat := &inventory.Catalog{Roles: map[string]*inventory.Role{
		"authelia": {Variables: map[string]any{
			"authelia_role_depends_on": "{{ 'authelia-redis,lldap' if (lookup('role_var', '_x', role='authelia') == 'ldap') else 'authelia-redis' }}",
		}},
	}}
	got := dependsNames(cat, map[string]any{}, "authelia")
	if !got["authelia-redis"] || !got["lldap"] {
		t.Fatalf("expected authelia-redis + lldap, got %v", keys(got))
	}
}

func TestDeclaredNames(t *testing.T) {
	cat := &inventory.Catalog{Roles: map[string]*inventory.Role{
		"n8n": {Variables: map[string]any{
			"n8n_role_postgres_deploy": true,
			"n8n_role_postgres_name":   "{{ n8n_name }}-postgres",
		}},
	}}
	got := declaredNames(cat, map[string]any{}, "n8n", []string{"n8n"})
	if !got["n8n-postgres"] || len(got) != 1 {
		t.Fatalf("declared=%v", keys(got))
	}
	// deploy:false → skipped
	cat.Roles["n8n"].Variables["n8n_role_redis_deploy"] = false
	cat.Roles["n8n"].Variables["n8n_role_redis_name"] = "{{ n8n_name }}-redis"
	got = declaredNames(cat, map[string]any{}, "n8n", []string{"n8n"})
	if got["n8n-redis"] {
		t.Fatalf("redis deploy:false should be skipped: %v", keys(got))
	}
}

func TestTitleCase(t *testing.T) {
	if titleCase("docker-socket-proxy") != "Docker Socket Proxy" {
		t.Fatalf("got %q", titleCase("docker-socket-proxy"))
	}
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
