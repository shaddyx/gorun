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

# Idempotently ensure $gobin is on PATH by writing an export line to the
# user's shell rc file(s). Safe to run repeatedly: it never duplicates lines.
ensure_path() {
    local gobin="$1"
    local line="export PATH=\"\$PATH:$gobin\""

    # Candidate rc files, most specific shell first, deduped, existing only.
    local files=()
    local f
    for f in \
        "${ZDOTDIR:-$HOME}/.zshrc" \
        "$HOME/.bashrc" \
        "$HOME/.bash_profile" \
        "$HOME/.profile"; do
        [[ -f "$f" ]] || continue
        local seen=0
        local p
        for p in "${files[@]}"; do
            [[ "$p" == "$f" ]] && seen=1
        done
        (( seen )) || files+=("$f")
    done

    if (( ${#files[@]} == 0 )); then
        warn "no shell rc file found; add it manually with:  $line"
        return 0
    fi

    local rc
    for rc in "${files[@]}"; do
        # Idempotent: skip if the exact line (or a matching PATH entry) exists.
        if grep -qF "$line" "$rc" 2>/dev/null; then
            log "$rc already has $gobin on PATH"
            continue
        fi
        if grep -qE "PATH=.*(^|:)$gobin(:|$)" "$rc" 2>/dev/null; then
            log "$rc already has $gobin on PATH"
            continue
        fi

        printf '\n# added by gorun installer\n%s\n' "$line" >>"$rc"
        log "added $gobin to PATH in $rc"
    done
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
        ensure_path "$gobin"
        warn "restart your shell or run:  source ~/.bashrc"
    fi

    log "done. run 'gorun --help' to get started"
}

main "$@"
