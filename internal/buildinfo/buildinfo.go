// Package buildinfo holds the binary version, injected at build time via
// -ldflags "-X sb-ui/internal/buildinfo.Version=...". Defaults to "dev".
package buildinfo

// Version is the release version (e.g. "v0.7.0"). Overridden by the linker.
var Version = "dev"
