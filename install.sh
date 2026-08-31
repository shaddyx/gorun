#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

readonly REPO="github.com/shaddyx/gorun"
readonly VERSION="${GORUN_VERSION:-latest}"
readonly MIN_GO_MAJOR=1
readonly MIN_GO_MINOR=26

log()  { printf '\033[1;32mgorun:\033[0m %s\n' "$*" >&2; }
warn() { printf '\033[1;33mgorun:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mgorun:\033[0m %s\n' "$*" >&2; exit 1; }

require_go() {
    if ! command -v go >/dev/null 2>&1; then
        die "Go is required but was not found.

  Install Go $MIN_GO_MAJOR.$MIN_GO_MINOR or newer:
    - Linux/macOS:  https://go.dev/dl/  (or your package manager, e.g. 'apt install golang-go')
    - macOS (brew): brew install go
    - Windows:      https://go.dev/dl/  (or 'winget install GoLang.Go')
  Then re-run this installer."
    fi

    local ver major minor
    ver="$(go env GOVERSION 2>/dev/null || go version)"
    ver="${ver#go}"
    major="${ver%%.*}"
    minor="${ver#*.}"
    minor="${minor%%.*}"

    if [[ "$major" -lt "$MIN_GO_MAJOR" ]] || { [[ "$major" -eq "$MIN_GO_MAJOR" ]] && [[ "$minor" -lt "$MIN_GO_MINOR" ]]; }; then
        die "Go $ver is too old; gorun requires Go $MIN_GO_MAJOR.$MIN_GO_MINOR or newer.

  Upgrade Go from https://go.dev/dl/ (or your package manager), then re-run this installer."
    fi
}

require_git() {
    if ! command -v git >/dev/null 2>&1; then
        die "git is required but was not found.

  Install git:
    - Debian/Ubuntu:  apt install git
    - Fedora:         dnf install git
    - macOS (brew):   brew install git
    - Windows:        https://git-scm.com/download/win
  Then re-run this installer."
    fi
}

main() {
    require_go
    require_git

    local gobin
    gobin="$(go env GOPATH)/bin"
    mkdir -p "$gobin"

    log "installing $REPO@$VERSION into $gobin"
    if ! GOBIN="$gobin" go install "$REPO@$VERSION"; then
        die "go install failed"
    fi

    local bin="$gobin/gorun"
    [[ -x "$bin" ]] || die "installed binary not found at $bin"

    log "installed $(basename "$bin")"

    if ! command -v gorun >/dev/null 2>&1; then
        warn "$gobin is not on your PATH"
        warn "add it with:  export PATH=\"\$PATH:$gobin\""
    fi

    log "done. run 'gorun --help' to get started"
}

main "$@"
