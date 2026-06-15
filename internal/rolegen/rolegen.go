// Package rolegen generates a Sandbox role's defaults/tasks from a spec and
// writes it (+ patches sandbox.yml). Port of role_generator.py.
package rolegen

import (
	"context"
	"strings"
	"time"

	"sb-ui/internal/config"
	"sb-ui/internal/executor"
)

type Spec struct {
	Name        string              `json:"name"`
	DockerImage string              `json:"docker_image"`
	DockerTag   string              `json:"docker_tag"`
	Port        string              `json:"port"`
	Subdomain   string              `json:"subdomain"`
	Volumes     []map[string]string `json:"volumes"`
	EnvVars     []map[string]string `json:"env_vars"`
	AuthMode    string              `json:"auth_mode"`
}

var ssoMap = map[string]string{
	"sso":    "{{ traefik_default_sso_middleware }}",
	"bypass": "{{ traefik_default_middleware_bypass_auth }}",
	"none":   "",
}

const defaultsTmpl = `################################
# Basics
################################

__N___name: __N__

################################
# Paths
################################

__N___role_paths_folder: "{{ __N___name }}"
__N___role_paths_location: "{{ server_appdata_path }}/{{ __N___role_paths_folder }}"
__N___role_paths_folders_list:
  - "{{ __N___role_paths_location }}"

################################
# Web
################################

__N___role_web_subdomain: "__SUB__"
__N___role_web_domain: "{{ user.domain }}"
__N___role_web_port: "__PORT__"
__N___role_web_url: "{{ 'https://' + (lookup('role_var', '_web_subdomain', role='__N__') + '.' + lookup('role_var', '_web_domain', role='__N__')) }}"

################################
# Traefik
################################

__N___role_traefik_sso_middleware: "__SSO__"
__N___role_traefik_middleware_default: "{{ traefik_default_middleware }}"
__N___role_traefik_certresolver: "{{ traefik_default_certresolver }}"
__N___role_traefik_middleware_custom: ""
__N___role_traefik_enabled: true

################################
# Docker
################################

__N___role_docker_container: "{{ __N___name }}"
__N___role_docker_image_repo: "__IMG__"
__N___role_docker_image_tag: "__TAG__"
__N___role_docker_image: "{{ lookup('role_var', '_docker_image_repo', role='__N__') }}:{{ lookup('role_var', '_docker_image_tag', role='__N__') }}"
__N___role_docker_envs_default:
__ENVS__
__N___role_docker_envs_custom: {}
__N___role_docker_envs: "{{ lookup('role_var', '_docker_envs_default', role='__N__') | combine(lookup('role_var', '_docker_envs_custom', role='__N__')) }}"
__N___role_docker_volumes_default:
__VOLS__
__N___role_docker_volumes_custom: []
__N___role_docker_volumes: "{{ lookup('role_var', '_docker_volumes_default', role='__N__') + lookup('role_var', '_docker_volumes_custom', role='__N__') }}"
__N___role_docker_hostname: "{{ __N___name }}"
__N___role_docker_networks_alias: "{{ __N___name }}"
__N___role_docker_networks_default: []
__N___role_docker_networks_custom: []
__N___role_docker_networks: "{{ docker_networks_common + lookup('role_var', '_docker_networks_default', role='__N__') + lookup('role_var', '_docker_networks_custom', role='__N__') }}"
__N___role_docker_restart_policy: unless-stopped
__N___role_docker_state: started
__N___role_docker_user: "{{ uid }}:{{ gid }}"
`

const tasksTmpl = `---
- name: Add DNS record
  ansible.builtin.include_tasks: "{{ resources_tasks_path }}/dns/tasker.yml"
  vars:
    dns_record: "{{ lookup('role_var', '_dns_record') }}"
    dns_zone: "{{ lookup('role_var', '_dns_zone') }}"
    dns_proxy: "{{ lookup('role_var', '_dns_proxy') }}"

- name: Remove existing Docker container
  ansible.builtin.include_tasks: "{{ resources_tasks_path }}/docker/remove_docker_container.yml"

- name: Create directories
  ansible.builtin.include_tasks: "{{ resources_tasks_path }}/directories/create_directories.yml"

- name: Create Docker container
  ansible.builtin.include_tasks: "{{ resources_tasks_path }}/docker/create_docker_container.yml"
`

func GenerateDefaults(s Spec) string {
	n := s.Name
	sub := s.Subdomain
	if sub == "" {
		sub = n
	}
	tag := s.DockerTag
	if tag == "" {
		tag = "latest"
	}

	// envs
	var envLines []string
	for _, ev := range s.EnvVars {
		envLines = append(envLines, "  "+ev["key"]+`: "`+ev["value"]+`"`)
	}
	envsBlock := `  TZ: "{{ tz }}"`
	if len(envLines) > 0 {
		envsBlock = `  TZ: "{{ tz }}"` + "\n" + strings.Join(envLines, "\n")
	}

	// volumes
	var volLines []string
	for _, v := range s.Volumes {
		volLines = append(volLines, `  - "`+v["host"]+`:`+v["container"]+`"`)
	}
	volsBlock := `  - "{{ lookup('role_var', '_paths_location', role='` + n + `') }}:/data"`
	if len(volLines) > 0 {
		volsBlock = strings.Join(volLines, "\n")
	}

	out := defaultsTmpl
	out = strings.ReplaceAll(out, "__SUB__", sub)
	out = strings.ReplaceAll(out, "__PORT__", s.Port)
	out = strings.ReplaceAll(out, "__SSO__", ssoMap[orDefault(s.AuthMode, "sso")])
	out = strings.ReplaceAll(out, "__IMG__", s.DockerImage)
	out = strings.ReplaceAll(out, "__TAG__", tag)
	out = strings.ReplaceAll(out, "__ENVS__", envsBlock)
	out = strings.ReplaceAll(out, "__VOLS__", volsBlock)
	out = strings.ReplaceAll(out, "__N__", n) // last: substitutes role name everywhere
	return out
}

func GenerateTasks(Spec) string { return tasksTmpl }

func WriteRole(s Spec) error {
	e := executor.Get()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base := config.Get().SandboxRepo + "/roles/" + s.Name
	if err := e.MakeDirs(ctx, base+"/tasks"); err != nil {
		return err
	}
	if err := e.MakeDirs(ctx, base+"/defaults"); err != nil {
		return err
	}
	if err := e.WriteFile(ctx, base+"/defaults/main.yml", GenerateDefaults(s)); err != nil {
		return err
	}
	return e.WriteFile(ctx, base+"/tasks/main.yml", GenerateTasks(s))
}

func PatchSandboxYml(name string) error {
	e := executor.Get()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	yml := config.Get().SandboxPlaybook()
	if ok, _ := e.FileExists(ctx, yml); !ok {
		return nil
	}
	content, err := e.ReadFile(ctx, yml)
	if err != nil {
		return err
	}
	if strings.Contains(content, name) {
		return nil
	}
	entry := "    - { role: " + name + ", tags: ['" + name + "'] }\n"
	marker := "    # Apps End"
	if strings.Contains(content, marker) {
		content = strings.Replace(content, marker, entry+marker, 1)
	} else {
		content += "\n" + entry
	}
	return e.WriteFile(ctx, yml, content)
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
