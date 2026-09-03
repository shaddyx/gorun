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
