// Package rcloneexec owns how rclone is invoked: it renders flags from typed values,
// assembles the argv for a transfer, and says what an exit code means.
//
// It is the bottom layer of the transfer stack and deliberately has no idea *why* a
// transfer is happening. Nothing above it may build an rclone argument itself — that rule
// is the whole point of the package. The daily-cap bug that once pushed a terabyte past a
// 500 G limit happened because a byte count was formatted into a flag at the call site,
// where nothing recorded that rclone reads a bare number as KiB.
//
// Everything here is pure: no process is started, no state is touched, so the exact
// command a run will execute can be asserted in a test, and previewed in the UI without
// running anything.
package rcloneexec

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

// ── typed values ──────────────────────────────────────────────────────────────

// Bytes is a byte count on its way to an rclone flag. Wrap a count in it rather than
// formatting the number, so the unit cannot be lost in a string.
type Bytes int64

// Flag renders the count for a size-valued rclone flag such as --max-transfer.
//
// The "B" suffix is mandatory and is why this type exists: rclone parses a bare number as
// KiB, which silently multiplies any limit by 1024. Never format a byte count for a flag
// any other way.
func (b Bytes) Flag() string { return strconv.FormatInt(int64(b), 10) + "B" }

// ── exit codes ────────────────────────────────────────────────────────────────

// rclone's documented exit codes for the two outcomes that are not failures.
const (
	// ExitMaxTransfer: "transfer exceeded — limit set by --max-transfer reached". When a
	// caller caps a run, this is how a successful capped run ends.
	ExitMaxTransfer = 8
	// ExitNoTransfer: "operation successful, but no files transferred".
	ExitNoTransfer = 9
)

// ClassifyExit says what an rclone exit code means for the caller: whether the run failed,
// and whether it stopped because a --max-transfer limit was reached.
//
// Reaching the limit is a designed stop, not an error — under --cutoff-mode cautious rclone
// halts at a whole-file boundary once the allowance is spent. Treating every non-zero code
// as failure made correct capped runs report as failures.
func ClassifyExit(code int) (failed, capped bool) {
	switch code {
	case 0, ExitNoTransfer:
		return false, false
	case ExitMaxTransfer:
		return false, true
	default:
		return true, false
	}
}

// ── the transfer request ──────────────────────────────────────────────────────

// Item is one source endpoint: "remote:path" or an absolute local path.
type Item struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"` // dirs get their name appended to dest (rclone merges contents otherwise)
}

// ExtraFlag is a free-form rclone flag chosen from the flag browser. Value may be empty
// for boolean flags.
type ExtraFlag struct {
	Flag  string `json:"flag"`
	Value string `json:"value"`
}

// Opts mirrors the common rclone transfer flags. Only whitelisted fields are rendered —
// raw flag strings are never passed through, so a value cannot inject an argument.
type Opts struct {
	Transfers          int         `json:"transfers"`
	Checkers           int         `json:"checkers"`
	Bwlimit            string      `json:"bwlimit"`
	Tpslimit           int         `json:"tpslimit"`
	Retries            int         `json:"retries"`
	IgnoreExisting     bool        `json:"ignore_existing"`
	Update             bool        `json:"update"`
	CreateEmptySrcDirs bool        `json:"create_empty_src_dirs"`
	NoTraverse         bool        `json:"no_traverse"`
	OneFileSystem      bool        `json:"one_file_system"`
	FastList           bool        `json:"fast_list"`
	Compare            string      `json:"compare"`     // "" | checksum | size-only | ignore-size
	SyncDelete         string      `json:"sync_delete"` // during | after | before (sync only)
	Include            []string    `json:"include"`
	Exclude            []string    `json:"exclude"`
	Extra              []ExtraFlag `json:"extra"`
}

var (
	bwlimitRE = regexp.MustCompile(`^[0-9.]+[bBkKmMgGtTi]*(:[0-9.]+[bBkKmMgGtTi]*)?$`)
	flagRE    = regexp.MustCompile(`^--[a-z0-9][a-z0-9-]*$`)
)

// statsFlags are on every run: JSON logging so progress can be parsed, and 1 s stats.
var statsFlags = []string{"--use-json-log", "--stats", "1s", "--stats-file-name-length", "0", "-v"}

// Flags turns whitelisted opts into rclone argv. Out-of-range numbers and malformed
// bandwidth or flag names are dropped rather than passed on.
func Flags(op string, o Opts, dryRun bool) []string {
	var f []string
	add := func(name, val string) { f = append(f, name, val) }
	if dryRun {
		f = append(f, "--dry-run")
	}
	if o.Transfers > 0 && o.Transfers <= 64 {
		add("--transfers", strconv.Itoa(o.Transfers))
	}
	if o.Checkers > 0 && o.Checkers <= 64 {
		add("--checkers", strconv.Itoa(o.Checkers))
	}
	if o.Tpslimit > 0 && o.Tpslimit <= 1000 {
		add("--tpslimit", strconv.Itoa(o.Tpslimit))
	}
	if o.Retries > 0 && o.Retries <= 100 {
		add("--retries", strconv.Itoa(o.Retries))
	}
	if o.Bwlimit != "" && bwlimitRE.MatchString(o.Bwlimit) {
		add("--bwlimit", o.Bwlimit)
	}
	if o.IgnoreExisting {
		f = append(f, "--ignore-existing")
	}
	if o.Update {
		f = append(f, "--update")
	}
	if o.CreateEmptySrcDirs {
		f = append(f, "--create-empty-src-dirs")
	}
	if o.NoTraverse {
		f = append(f, "--no-traverse")
	}
	if o.OneFileSystem {
		f = append(f, "--one-file-system")
	}
	if o.FastList {
		f = append(f, "--fast-list")
	}
	switch o.Compare {
	case "checksum":
		f = append(f, "--checksum")
	case "size-only":
		f = append(f, "--size-only")
	case "ignore-size":
		f = append(f, "--ignore-size")
	}
	if op == "sync" {
		switch o.SyncDelete {
		case "after":
			f = append(f, "--delete-after")
		case "before":
			f = append(f, "--delete-before")
		case "during":
			f = append(f, "--delete-during")
		}
	}
	for _, p := range o.Include {
		if p = strings.TrimSpace(p); p != "" && !strings.ContainsAny(p, "\n\r") {
			add("--include", p)
		}
	}
	for _, p := range o.Exclude {
		if p = strings.TrimSpace(p); p != "" && !strings.ContainsAny(p, "\n\r") {
			add("--exclude", p)
		}
	}
	for _, e := range o.Extra {
		if !flagRE.MatchString(e.Flag) || strings.ContainsAny(e.Value, "\n\r") {
			continue
		}
		if e.Value == "" {
			f = append(f, e.Flag)
		} else {
			add(e.Flag, e.Value)
		}
	}
	return f
}

// Argv assembles the full command line(s) for one transfer request — the exact argv the
// runner will execute, and the same thing the UI can preview.
//
// Selected items are grouped by their parent directory and each group becomes ONE rclone
// command driven by --filter rules, so rclone moves the group in parallel while preserving
// each item's own name under the destination. Items from different parents (or different
// remotes) cannot share a command, so they come back as separate argvs to run in sequence.
// Group order follows the order the items were given.
func Argv(conf, op string, items []Item, dst string, dryRun bool, o Opts) [][]string {
	flags := Flags(op, o, dryRun)

	order := []string{}
	groups := map[string][]string{}
	for _, it := range items {
		p := Parent(it.Path)
		if _, ok := groups[p]; !ok {
			order = append(order, p)
		}
		groups[p] = append(groups[p], Base(it.Path))
	}

	out := make([][]string, 0, len(order))
	for _, parent := range order {
		args := []string{"rclone", "--config", conf, op, parent, dst}
		args = append(args, statsFlags...)
		args = append(args, flags...)
		for _, n := range groups[parent] {
			args = append(args, "--filter", "+ /"+n, "--filter", "+ /"+n+"/**")
		}
		// Anything not named above is excluded, so a group moves exactly its own items.
		args = append(args, "--filter", "- *")
		out = append(out, args)
	}
	return out
}

// ── endpoint paths ────────────────────────────────────────────────────────────

// Parent returns the containing directory of a "remote:path" or "/local/path", keeping the
// remote prefix attached.
func Parent(p string) string {
	if i := strings.Index(p, ":"); i > 0 && !strings.HasPrefix(p, "/") {
		base, rest := p[:i+1], p[i+1:]
		if j := strings.LastIndex(rest, "/"); j >= 0 {
			return base + rest[:j]
		}
		return base
	}
	if j := strings.LastIndex(p, "/"); j > 0 {
		return p[:j]
	}
	return "/"
}

// Base returns the final path element of a "remote:path" or "/local/path".
func Base(p string) string {
	if i := strings.Index(p, ":"); i > 0 && !strings.HasPrefix(p, "/") {
		return path.Base(p[i+1:])
	}
	return path.Base(p)
}
