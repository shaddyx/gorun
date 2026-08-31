#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

readonly REPO="github.com/shaddyx/gorun"
readonly VERSION="${GORUN_VERSION:-latest}"

log()  { printf '\033[1;32mgorun:\033[0m %s\n' "$*" >&2; }
warn() { printf '\033[1;33mgorun:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mgorun:\033[0m %s\n' "$*" >&2; exit 1; }

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

main() {
    require_cmd go
    require_cmd git

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
