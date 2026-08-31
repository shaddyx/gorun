package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
