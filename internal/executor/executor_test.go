package executor

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLocalRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX echo")
	}
	e := LocalExecutor{}
	rc, out, err := e.Run(context.Background(), []string{"echo", "hello"}, "")
	if err != nil || rc != 0 || out != "hello\n" {
		t.Fatalf("Run: rc=%d out=%q err=%v", rc, out, err)
	}
}

func TestLocalRunExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX false")
	}
	e := LocalExecutor{}
	rc, _, err := e.Run(context.Background(), []string{"false"}, "")
	if err != nil || rc != 1 {
		t.Fatalf("expected rc=1 err=nil, got rc=%d err=%v", rc, err)
	}
}

func TestLocalFileOps(t *testing.T) {
	e := LocalExecutor{}
	ctx := context.Background()
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "file.txt")

	if err := e.MakeDirs(ctx, filepath.Dir(p)); err != nil {
		t.Fatal(err)
	}
	if err := e.WriteFile(ctx, p, "data\nhere"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.FileExists(ctx, p); !ok {
		t.Fatal("FileExists should be true")
	}
	if ok, _ := e.FileExists(ctx, p+".nope"); ok {
		t.Fatal("FileExists should be false")
	}
	got, err := e.ReadFile(ctx, p)
	if err != nil || got != "data\nhere" {
		t.Fatalf("ReadFile: %q err=%v", got, err)
	}
}

func TestLocalRunStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX printf")
	}
	e := LocalExecutor{}
	s, err := e.RunStream(context.Background(), []string{"printf", "a\nb\nc\n"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for line := range s.Lines {
		got = append(got, line)
	}
	if s.Exit() != 0 {
		t.Fatalf("exit=%d", s.Exit())
	}
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("lines=%v", got)
	}
}

// Portable: exercise Run/RunStdout/RunStream using the `go` binary (always present).
func TestLocalRunPortable(t *testing.T) {
	e := LocalExecutor{}
	ctx := context.Background()

	rc, out, err := e.Run(ctx, []string{"go", "version"}, "")
	if err != nil || rc != 0 || !contains(out, "go") {
		t.Fatalf("Run go version: rc=%d out=%q err=%v", rc, out, err)
	}

	rc, out, err = e.RunStdout(ctx, []string{"go", "env", "GOOS"}, "")
	if err != nil || rc != 0 || len(out) == 0 {
		t.Fatalf("RunStdout: rc=%d out=%q err=%v", rc, out, err)
	}

	s, err := e.RunStream(ctx, []string{"go", "env", "GOOS"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for l := range s.Lines {
		lines = append(lines, l)
	}
	if s.Exit() != 0 || len(lines) == 0 {
		t.Fatalf("RunStream: exit=%d lines=%v", s.Exit(), lines)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestCleanPTY(t *testing.T) {
	in := "\x1b[0;32mok\x1b[0m\r\ndone\r"
	if out := cleanPTY(in); out != "ok\ndone\n" {
		t.Fatalf("cleanPTY=%q", out)
	}
}

func TestShQuote(t *testing.T) {
	cases := map[string]string{"abc": "abc", "a b": "'a b'", "": "''", "a'b": `'a'\''b'`}
	for in, want := range cases {
		if got := shQuote(in); got != want {
			t.Errorf("shQuote(%q)=%q want %q", in, got, want)
		}
	}
}
