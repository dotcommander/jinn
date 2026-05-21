# List available recipes
default:
    @just --list

# Build jinn (repo root)
build:
    go build -ldflags "-X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o jinn ./cmd/jinn/

# Build and symlink into ~/go/bin/jinn
install: build
    mkdir -p ~/go/bin
    ln -sf "$(pwd)/jinn" ~/go/bin/jinn

# Run the full test suite with race detector
test:
    go test -race ./...

# Re-run the related_context regression test (client=pi source inclusion)
related-context-test:
    bash scripts/related-context-test.sh

# Remove the local jinn binary
clean:
    rm -f ./jinn

# Print current version (latest git tag, sans v prefix)
version:
    @git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || echo "0.0.0"

# Force-clean after /cpt leaves dirt — add everything, amend, push, retag.
cpt-clean:
    #!/usr/bin/env bash
    set -euo pipefail
    if git diff --quiet && git diff --cached --quiet && [ -z "$(git ls-files --others --exclude-standard)" ]; then
        echo "✓ workspace clean"
        exit 0
    fi
    echo "⚠ dirty after /cpt — grabbing everything"
    git status --short
    git add -A
    git commit --amend --no-edit
    git push origin main --force-with-lease
    # Retag — old tag points to pre-amend commit
    TAG=$(git describe --tags --abbrev=0 2>/dev/null || true)
    if [ -n "$TAG" ]; then
        TAG_COMMIT=$(git rev-list -1 "$TAG")
        HEAD_COMMIT=$(git rev-parse HEAD)
        if [ "$TAG_COMMIT" != "$HEAD_COMMIT" ]; then
            git tag -f "$TAG" HEAD
            git push origin "$TAG" --force
        fi
    fi
    echo "✓ workspace clean"
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
