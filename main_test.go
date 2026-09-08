package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestNormalizePkgPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "dot slash", in: "./cmd/x", want: "cmd/x"},
		{name: "leading slash", in: "/cmd/x", want: "cmd/x"},
		{name: "trailing slash", in: "cmd/x/", want: "cmd/x"},
		{name: "both slashes", in: "/cmd/x/", want: "cmd/x"},
		{name: "plain", in: "cmd/x", want: "cmd/x"},
		{name: "empty", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizePkgPath(tt.in); got != tt.want {
				t.Errorf("normalizePkgPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsMainPackageFile(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "package main", content: "package main\n\nfunc main() {}\n", want: true},
		{name: "package other", content: "package foo\n\nfunc x() {}\n", want: false},
		{name: "build tag then main", content: "//go:build linux\n\npackage main\n", want: true},
		{name: "comment then main", content: "// comment\n\npackage main\n", want: true},
		{name: "indented main", content: "   package main\n", want: true},
		{name: "empty", content: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(dir, tt.name+".go")
			writeTestFile(t, p, tt.content)
			if got := isMainPackageFile(p); got != tt.want {
				t.Errorf("isMainPackageFile(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestIsMainPackageDir(t *testing.T) {
	t.Run("with main file", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "main.go"), "package main\n")
		if !isMainPackageDir(dir) {
			t.Error("expected main package dir")
		}
	})
	t.Run("ignores test files", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "main_test.go"), "package main\n")
		if isMainPackageDir(dir) {
			t.Error("expected non-main dir (only test file)")
		}
	})
	t.Run("ignores underscore file", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "_main.go"), "package main\n")
		if isMainPackageDir(dir) {
			t.Error("expected non-main dir (underscore file)")
		}
	})
	t.Run("non-main", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "lib.go"), "package lib\n")
		if isMainPackageDir(dir) {
			t.Error("expected non-main dir")
		}
	})
}

func TestFindMainPackageDirs(t *testing.T) {
	t.Run("root only", func(t *testing.T) {
		src := t.TempDir()
		writeTestFile(t, filepath.Join(src, "main.go"), "package main\n")
		got, err := findMainPackageDirs(src)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(got) != 1 || got[0] != "" {
			t.Errorf("got %v, want [\"\"]", got)
		}
	})

	t.Run("cmd subdir only", func(t *testing.T) {
		src := t.TempDir()
		writeTestFile(t, filepath.Join(src, "internal.go"), "package internal\n")
		cmdDir := filepath.Join(src, "cmd", "lazy-skill-mcp")
		if err := os.MkdirAll(cmdDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(cmdDir, "main.go"), "package main\n")
		got, err := findMainPackageDirs(src)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(got) != 1 || got[0] != "cmd/lazy-skill-mcp" {
			t.Errorf("got %v, want [cmd/lazy-skill-mcp]", got)
		}
	})

	t.Run("root and cmd both main", func(t *testing.T) {
		src := t.TempDir()
		writeTestFile(t, filepath.Join(src, "main.go"), "package main\n")
		cmdDir := filepath.Join(src, "cmd", "tool")
		if err := os.MkdirAll(cmdDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(cmdDir, "main.go"), "package main\n")
		got, err := findMainPackageDirs(src)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(got) != 2 || got[0] != "" || got[1] != "cmd/tool" {
			t.Errorf("got %v, want [\"\", cmd/tool]", got)
		}
	})

	t.Run("no main package", func(t *testing.T) {
		src := t.TempDir()
		writeTestFile(t, filepath.Join(src, "lib.go"), "package lib\n")
		got, err := findMainPackageDirs(src)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})

	t.Run("skips vendor and testdata and hidden", func(t *testing.T) {
		src := t.TempDir()
		for _, d := range []string{"vendor/x", "testdata/skills", ".hidden"} {
			if err := os.MkdirAll(filepath.Join(src, d), 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(src, d, "main.go"), "package main\n")
		}
		got, err := findMainPackageDirs(src)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty (vendor/testdata/hidden skipped)", got)
		}
	})
}

func TestPickMainPackage(t *testing.T) {
	t.Run("single auto-detected", func(t *testing.T) {
		src := t.TempDir()
		cmdDir := filepath.Join(src, "cmd", "app")
		if err := os.MkdirAll(cmdDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(cmdDir, "main.go"), "package main\n")
		got, err := pickMainPackage(src, "", "")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got != "cmd/app" {
			t.Errorf("got %q, want cmd/app", got)
		}
	})

	t.Run("multiple prefers root", func(t *testing.T) {
		src := t.TempDir()
		writeTestFile(t, filepath.Join(src, "main.go"), "package main\n")
		cmdDir := filepath.Join(src, "cmd", "other")
		if err := os.MkdirAll(cmdDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(cmdDir, "main.go"), "package main\n")
		got, err := pickMainPackage(src, "", "")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got != "" {
			t.Errorf("got %q, want \"\" (root)", got)
		}
	})

	t.Run("multiple no root errors", func(t *testing.T) {
		src := t.TempDir()
		for _, sub := range []string{"cmd/a", "cmd/b"} {
			d := filepath.Join(src, sub)
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(d, "main.go"), "package main\n")
		}
		if _, err := pickMainPackage(src, "", ""); err == nil {
			t.Error("expected error for multiple non-root main packages")
		}
	})

	t.Run("explicit wins", func(t *testing.T) {
		src := t.TempDir()
		for _, sub := range []string{"cmd/a", "cmd/b"} {
			d := filepath.Join(src, sub)
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(d, "main.go"), "package main\n")
		}
		got, err := pickMainPackage(src, "cmd/b", "")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got != "cmd/b" {
			t.Errorf("got %q, want cmd/b", got)
		}
	})

	t.Run("explicit invalid dir", func(t *testing.T) {
		src := t.TempDir()
		if _, err := pickMainPackage(src, "cmd/nope", ""); err == nil {
			t.Error("expected error for non-existent explicit dir")
		}
	})

	t.Run("explicit non-main dir", func(t *testing.T) {
		src := t.TempDir()
		d := filepath.Join(src, "lib")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(d, "lib.go"), "package lib\n")
		if _, err := pickMainPackage(src, "lib", ""); err == nil {
			t.Error("expected error for non-main explicit dir")
		}
	})

	t.Run("persisted reused when valid", func(t *testing.T) {
		src := t.TempDir()
		for _, sub := range []string{"cmd/a", "cmd/b"} {
			d := filepath.Join(src, sub)
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(d, "main.go"), "package main\n")
		}
		got, err := pickMainPackage(src, "", "cmd/a")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got != "cmd/a" {
			t.Errorf("got %q, want cmd/a (persisted)", got)
		}
	})

	t.Run("persisted falls back when invalid", func(t *testing.T) {
		src := t.TempDir()
		cmdDir := filepath.Join(src, "cmd", "only")
		if err := os.MkdirAll(cmdDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(cmdDir, "main.go"), "package main\n")
		got, err := pickMainPackage(src, "", "cmd/gone")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got != "cmd/only" {
			t.Errorf("got %q, want cmd/only (fallback)", got)
		}
	})

	t.Run("no main package errors", func(t *testing.T) {
		src := t.TempDir()
		writeTestFile(t, filepath.Join(src, "lib.go"), "package lib\n")
		if _, err := pickMainPackage(src, "", ""); err == nil {
			t.Error("expected error when no main package exists")
		}
	})
}

func TestReadBuildPath(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		dir := t.TempDir()
		if got := readBuildPath(dir); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
	t.Run("reads and trims", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "buildpath"), "  cmd/x \n")
		if got := readBuildPath(dir); got != "cmd/x" {
			t.Errorf("got %q, want cmd/x", got)
		}
	})
}

func TestCacheKey(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "github https",
			url:  "https://github.com/user/repo.git",
			want: "cb1fdf79c83e1634f3ce487b7813541d2d7fc345ba3be71cc1d85fc3b6f41474",
		},
		{
			name: "empty",
			url:  "",
			want: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cacheKey(tt.url)
			if got != tt.want {
				t.Errorf("cacheKey(%q) = %q, want %q", tt.url, got, tt.want)
			}
			if len(got) != 64 {
				t.Errorf("cacheKey(%q) length = %d, want 64", tt.url, len(got))
			}
		})
	}
}

func TestCacheKeyDeterministic(t *testing.T) {
	url := "https://github.com/user/repo.git"
	if cacheKey(url) != cacheKey(url) {
		t.Error("cacheKey is not deterministic for the same input")
	}
}

func TestParseRef(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantURL string
		wantRef string
	}{
		{name: "no ref", url: "https://github.com/user/repo", wantURL: "https://github.com/user/repo", wantRef: ""},
		{name: "tag", url: "https://github.com/user/repo@v1.0.2", wantURL: "https://github.com/user/repo", wantRef: "v1.0.2"},
		{name: "tag with .git", url: "https://github.com/user/repo.git@v1.0.1", wantURL: "https://github.com/user/repo.git", wantRef: "v1.0.1"},
		{name: "branch", url: "https://github.com/user/repo@dev", wantURL: "https://github.com/user/repo", wantRef: "dev"},
		{name: "commit sha", url: "https://github.com/user/repo@abc123def", wantURL: "https://github.com/user/repo", wantRef: "abc123def"},
		{name: "ssh userinfo not split", url: "git@github.com:user/repo.git", wantURL: "git@github.com:user/repo.git", wantRef: ""},
		{name: "ssh with @ref", url: "git@github.com:user/repo.git@v1.0.1", wantURL: "git@github.com:user/repo.git", wantRef: "v1.0.1"},
		{name: "bare name", url: "repo", wantURL: "repo", wantRef: ""},
		{name: "empty", url: "", wantURL: "", wantRef: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotRef := parseRef(tt.url)
			if gotURL != tt.wantURL || gotRef != tt.wantRef {
				t.Errorf("parseRef(%q) = (%q, %q), want (%q, %q)", tt.url, gotURL, gotRef, tt.wantURL, tt.wantRef)
			}
		})
	}
}

func TestParseSemverTag(t *testing.T) {
	tests := []struct {
		tag    string
		want   semver
		wantOK bool
	}{
		{tag: "v1.2.3", want: semver{1, 2, 3, ""}, wantOK: true},
		{tag: "1.2.3", want: semver{1, 2, 3, ""}, wantOK: true},
		{tag: "v1.2.3-rc1", want: semver{1, 2, 3, "rc1"}, wantOK: true},
		{tag: "v2.0.0+build5", want: semver{2, 0, 0, ""}, wantOK: true},
		{tag: "v1.2", wantOK: false},
		{tag: "release-2024", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			got, ok := parseSemverTag(tt.tag)
			if ok != tt.wantOK {
				t.Fatalf("parseSemverTag(%q) ok = %v, want %v", tt.tag, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("parseSemverTag(%q) = %v, want %v", tt.tag, got, tt.want)
			}
		})
	}
}

func TestHighestSemver(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want string
	}{
		{name: "plain", tags: []string{"v0.1.3", "v1.0.0", "v1.2.0"}, want: "v1.2.0"},
		{name: "major beats minor", tags: []string{"v2.0.0", "v1.9.9"}, want: "v2.0.0"},
		{name: "release beats prerelease", tags: []string{"v1.9.0", "v2.0.0-rc1"}, want: "v1.9.0"},
		{name: "prerelease order", tags: []string{"v2.0.0-rc1", "v2.0.0-rc2"}, want: "v2.0.0-rc2"},
		{name: "only prereleases", tags: []string{"v1.0.0-rc1", "v1.0.0-rc2"}, want: "v1.0.0-rc2"},
		{name: "non-semver ignored", tags: []string{"release-2024", "v1.0.0"}, want: "v1.0.0"},
		{name: "none", tags: []string{"release-2024"}, want: ""},
		{name: "empty", tags: nil, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := highestSemver(tt.tags); got != tt.want {
				t.Errorf("highestSemver(%v) = %q, want %q", tt.tags, got, tt.want)
			}
		})
	}
}

func TestParseLsRemote(t *testing.T) {
	out := []byte(
		"ref: refs/heads/main\tHEAD\n" +
			"e64ec7a3ad1ef8bc828fe61e1fb324cc2e74c604\tHEAD\n" +
			"1111111111111111111111111111111111111111\trefs/tags/v0.1.3\n" +
			"2222222222222222222222222222222222222222\trefs/tags/v0.1.3^{}\n" +
			"3333333333333333333333333333333333333333\trefs/tags/v1.2.0\n" +
			"4444444444444444444444444444444444444444\trefs/tags/v1.2.0^{}\n")
	tags, def := parseLsRemote(out)
	if def != "main" {
		t.Errorf("default branch = %q, want main", def)
	}
	want := []string{"v0.1.3", "v1.2.0"}
	if len(tags) != len(want) {
		t.Fatalf("tags = %v, want %v", tags, want)
	}
	for i := range want {
		if tags[i] != want[i] {
			t.Errorf("tags[%d] = %q, want %q", i, tags[i], want[i])
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "schemeless", url: "github.com/user/repo", want: "https://github.com/user/repo"},
		{name: "https unchanged", url: "https://github.com/user/repo", want: "https://github.com/user/repo"},
		{name: "ssh unchanged", url: "git@github.com:user/repo.git", want: "git@github.com:user/repo.git"},
		{name: "git scheme unchanged", url: "git://example.com/user/repo", want: "git://example.com/user/repo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeURL(tt.url); got != tt.want {
				t.Errorf("normalizeURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestRepoName(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "https with .git", url: "https://github.com/user/repo.git", want: "repo"},
		{name: "https without .git", url: "https://github.com/user/repo", want: "repo"},
		{name: "trailing slash", url: "https://github.com/user/repo/", want: "repo"},
		{name: "ssh", url: "git@github.com:user/repo.git", want: "repo"},
		{name: "nested path", url: "https://github.com/org/team/project.git", want: "project"},
		{name: "versioned https", url: "https://github.com/user/repo@v1.0.2", want: "repo"},
		{name: "versioned ssh", url: "git@github.com:user/repo.git@v1.0.1", want: "repo"},
		{name: "bare name", url: "repo", want: "repo"},
		{name: "empty", url: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := repoName(tt.url); got != tt.want {
				t.Errorf("repoName(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestRepoNameFromDir(t *testing.T) {
	t.Run("no git remote falls back to base name", func(t *testing.T) {
		dir := t.TempDir()
		if got := repoNameFromDir(dir); got != filepath.Base(dir) {
			t.Errorf("repoNameFromDir(%q) = %q, want %q", dir, got, filepath.Base(dir))
		}
	})
}

func TestCacheRoot(t *testing.T) {
	t.Run("uses XDG_CACHE_HOME when set", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-cache")
		got, err := cacheRoot()
		if err != nil {
			t.Fatalf("cacheRoot() error = %v", err)
		}
		want := filepath.Join("/tmp/xdg-cache", "gorun")
		if got != want {
			t.Errorf("cacheRoot() = %q, want %q", got, want)
		}
	})

	t.Run("falls back to home .cache when XDG unset", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", "")
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("os.UserHomeDir() error = %v", err)
		}
		got, err := cacheRoot()
		if err != nil {
			t.Fatalf("cacheRoot() error = %v", err)
		}
		want := filepath.Join(home, ".cache", "gorun")
		if got != want {
			t.Errorf("cacheRoot() = %q, want %q", got, want)
		}
	})
}
