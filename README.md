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
```

The first positional argument is the git URL; everything after it is forwarded
verbatim to the application.

## Flags

| Flag            | Description                                                       |
| --------------- | ----------------------------------------------------------------- |
| `--upgrade`     | Force re-fetch (`git pull`) and rebuild, then run.                 |
| `--upgrade-all` | Re-query git and rebuild every cached project, then exit.         |
| `--clean`       | Wipe the entire gorun cache, then exit.                           |

`--clean` and `--upgrade-all` are mutually exclusive.

## How it works

1. The git URL is hashed (SHA-256) to form a cache key.
2. The project is cloned into `$XDG_CACHE_HOME/gorun/<key>/src`
   (default `~/.cache/gorun/<key>/src`).
3. The binary is built into `<key>/bin/<name>`.
4. On subsequent runs the cached binary is reused — no clone or build.
5. `--upgrade` re-pulls and rebuilds; `--upgrade-all` does this for every cached
   project.

Signals (SIGINT/SIGTERM) are forwarded to the running app, and its exit code is
propagated.

## License

MIT
