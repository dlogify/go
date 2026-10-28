#!/usr/bin/env bash
# Runs each adapter's tests against the latest release of its logging library
# (the adapters' go.mod files pin the oldest supported one).
set -euo pipefail
cd "$(dirname "$0")/.."
declare -A libraries=([zap]=go.uber.org/zap [zerolog]=github.com/rs/zerolog [logrus]=github.com/sirupsen/logrus)
status=0
for m in zap zerolog logrus; do
  (
    cd "$m"
    cp go.mod go.latest.mod
    cp go.sum go.latest.sum
    trap 'rm -f go.latest.mod go.latest.sum' EXIT
    go get -modfile=go.latest.mod "${libraries[$m]}@latest"
    go test -race -modfile=go.latest.mod ./...
  ) || status=1
done
exit "$status"
