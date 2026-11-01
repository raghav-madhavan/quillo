#!/usr/bin/env sh
# Build the Quillo images on the machine that will run them (for example the
# VPS), for its own CPU architecture. Run from the repository root.
#
#   quillo:base    the server alone (docker-compose.advanced.yml)
#   quillo:latest  the server with bundled PostgreSQL and Redis (docker-compose.yml)
set -eu
revision=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)
docker build --build-arg BUILD_REVISION="$revision" -t quillo:base .
docker build --build-arg SILO_IMAGE=quillo:base -t quillo:latest omnibus
echo "Built quillo:base and quillo:latest ($revision)"
