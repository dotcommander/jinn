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
