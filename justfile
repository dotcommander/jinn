# List available recipes
default:
    @just --list

# Build jinn (repo root)
build:
    go build -ldflags "-X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o jinn ./cmd/jinn/

# Resolve GOBIN, falling back to GOPATH/bin when it is unset.
gobin := `gobin="$(go env GOBIN)"; [ -n "$gobin" ] && echo "$gobin" || echo "$(go env GOPATH)/bin"`

# Build and symlink into the configured Go bin directory.
install: build
    mkdir -p "{{gobin}}"
    ln -sf "$(pwd)/jinn" "{{gobin}}/jinn"

# Run the full test suite with race detector
test:
    go test -race ./...
    GOWORK=off go -C examples/pi-go-tool-bridge/bin test -race ./...

# Remove the local jinn binary
clean:
    rm -f ./jinn

# Print current version (latest git tag, sans v prefix)
version:
    @git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || echo "0.0.0"

# Tag, push, and create GitHub release. Bump = patch | minor | major | X.Y.Z
release bump:
    #!/usr/bin/env bash
    set -euo pipefail
    CUR=$(just version)
    if [[ "{{ bump }}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      VER="{{ bump }}"
    else
      VER=$(echo "$CUR" | awk -F. -v b="{{ bump }}" '{
        if(b=="major") print $1+1".0.0"
        else if(b=="minor") print $1"."$2+1".0"
        else if(b=="patch") print $1"."$2"."$3+1
        else { print "bad bump: " b > "/dev/stderr"; exit 1 }
      }')
    fi
    echo "v$CUR → v$VER"
    just test
    mkdir -p "{{gobin}}"
    tmp=$(mktemp "{{gobin}}/.jinn-release.XXXXXX")
    trap 'rm -f "$tmp"' EXIT
    go build -ldflags "-X main.version=v$VER" -o "$tmp" ./cmd/jinn/
    [[ "$("$tmp" --version)" == "v$VER" ]] || { echo "release build version mismatch" >&2; exit 1; }
    mv -f "$tmp" "{{gobin}}/jinn"
    [[ "$("{{gobin}}/jinn" --version)" == "v$VER" ]] || { echo "installed release version mismatch" >&2; exit 1; }
    git tag "v$VER"
    git push origin main
    git push origin "v$VER"
    notes=(--generate-notes)
    if [[ -n "${JINN_RELEASE_NOTES:-}" ]]; then
      notes=(--notes-file "$JINN_RELEASE_NOTES")
    fi
    gh release create "v$VER" --repo dotcommander/jinn --title "v$VER" --verify-tag "${notes[@]}" --latest
    echo "✓ v$VER live"
