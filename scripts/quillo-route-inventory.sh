#!/usr/bin/env sh
# Regenerate contracts/api/v2/route-inventory.json on a non-Linux machine.
# The generator type-checks the tree as linux/cgo, so it runs in a Linux
# container with libvips headers. Go caches persist in named volumes.
# Run from the repository root. Pass --check to verify instead of write.
set -eu
mode="-out"
[ "${1:-}" = "--check" ] && mode="-check"
docker run --rm \
  -v "$PWD":/src -w /src \
  -v quillo-go-mod:/go/pkg/mod -v quillo-go-build:/root/.cache/go-build \
  -e GOFLAGS=-buildvcs=false \
  golang:1.26 sh -c "
    apt-get update -qq >/dev/null &&
    apt-get install -y -qq --no-install-recommends libvips-dev pkg-config >/dev/null &&
    go run ./cmd/route-inventory $mode contracts/api/v2/route-inventory.json"
