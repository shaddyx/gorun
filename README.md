# gorun

Run a Go application straight from a git URL — clone, build, run, and cache the binary.

```bash
gorun [flags] <git-url> [app-args...]
```

## Install

Requires **Go 1.26+** and **git**.

```bash
curl -fsSL https://raw.githubusercontent.com/shaddyx/gorun/master/install.sh | bash
```

Or install directly with Go:

```bash
go install github.com/shaddyx/gorun@latest
```

The binary is placed in `$(go env GOPATH)/bin`. If that directory is not on your
`PATH`, add it:

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
```

## Usage

```bash
# Run a tool from GitHub
gorun https://github.com/rakyll/hey -n 100 https://example.com

# Force a re-fetch (git pull) and rebuild, then run
gorun --upgrade https://github.com/rakyll/hey

# Re-query git and rebuild every cached project
gorun --upgrade-all

# Wipe the entire cache
gorun --clean

# Show the full process output (git clone/pull) without suppression
gorun --verbose https://github.com/rakyll/hey

# Pin a version with an @ref suffix (tag, branch, or commit SHA)
gorun github.com/user/repo@v1.0.2

# Track a specific branch
gorun github.com/user/repo@main

# Highest semver release (or the default branch if untagged);
# re-resolved only on --upgrade
gorun github.com/user/repo@latest

# Repos where the main package lives in a subdirectory are auto-detected:
# e.g. lazy-skills-mcp has its binary in cmd/lazy-skill-mcp
gorun github.com/shaddyx/lazy-skills-mcp --list

# If a repo has several main packages, pick one explicitly:
gorun --main cmd/mytool github.com/user/repo
```

The first positional argument is the git URL; everything after it is forwarded
verbatim to the application.

A trailing `@ref` (tag, branch, or commit SHA) pins the checkout: the repo is
cloned with `git clone --depth 1 --branch <ref>`, and `--upgrade` re-fetches and
resets back to that ref instead of `origin/HEAD`. Each distinct `@ref` gets its
own cache entry, so different versions can coexist.

`@latest` is special: it resolves to the highest semver release tag
(`v1.2.3`-style) in the remote, preferring releases over pre-releases. If the
remote has no semver tags, it falls back to the most recent commit on the
default branch. It maps to a single stable cache entry: the ref is resolved on
the first run and re-resolved only on `--upgrade` (or `--upgrade-all`), so a
plain re-run executes the cached binary with no network access. To track the
newest commit on a specific branch regardless of tags, use the branch name
instead (e.g. `@main` or `@master`).

Schemeless URLs (`github.com/user/repo`) are normalized to `https://` before
cloning, so both `github.com/user/repo@v1.0.2` and
`https://github.com/user/repo@v1.0.2` resolve to the same cache entry.

## Flags

| Flag            | Description                                                       |
| --------------- | ----------------------------------------------------------------- |
| `--upgrade`     | Force re-fetch (`git pull`) and rebuild, then run.                 |
| `--upgrade-all` | Re-query git and rebuild every cached project, then exit.         |
| `--clean`       | Wipe the entire gorun cache, then exit.                           |
| `--verbose`     | Show the full process output (git clone/pull) without suppression. |
| `--main <dir>`  | Module-relative path to the `main` package to build (default: auto-detect). |

`--clean` and `--upgrade-all` are mutually exclusive.

By default git output is suppressed and only shown if the git command fails.
Pass `--verbose` to stream it live.

## How it works

1. The git URL is hashed (SHA-256) to form a cache key. A trailing `@ref`
   (version/branch/commit) is included in the key, so each version is cached
   separately. `@latest` always maps to a single stable cache entry (keyed on
   the literal `@latest`); it is resolved on the first run and re-resolved only
   on `--upgrade`.
2. The project is cloned into `$XDG_CACHE_HOME/gorun/<key>/src`
   (default `~/.cache/gorun/<key>/src`); if an `@ref` is present the clone is
   pinned to it via `git clone --depth 1 --branch <ref>`.
3. The main package is located (auto-detected by scanning for `package main`,
   or specified with `--main`), then built into `<key>/bin/<name>`.
4. On subsequent runs the cached binary is reused — no clone or build.
5. `--upgrade` re-pulls and rebuilds; `--upgrade-all` does this for every cached
   project.

Signals (SIGINT/SIGTERM) are forwarded to the running app, and its exit code is
propagated.

## License

MIT
