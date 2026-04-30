# List available recipes
default:
    @just --list

# Build jinn (repo root) and the demo binary (examples/demo/demo)
build:
    go build -ldflags "-X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o jinn ./cmd/jinn/
    (cd examples/demo && go build -o demo .)

# Build and symlink into ~/go/bin/jinn
install: build
    mkdir -p ~/go/bin
    ln -sf "$(pwd)/jinn" ~/go/bin/jinn

# Run the full test suite with race detector
test:
    go test -race ./...

# Remove the local jinn binary and the demo binary
clean:
    rm -f ./jinn ./examples/demo/demo

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
    just build
    git push origin main
    git tag "v$VER"
    git push origin "v$VER"
    gh release create "v$VER" --repo dotcommander/jinn --title "v$VER" --generate-notes --latest
    echo "✓ v$VER live"
