#!/usr/bin/env bash
# Publishes dlogify.com/sdk/go and its adapters from the monorepo:
#   clients/go/scripts/release.sh v0.1.0
# The public mirror's root is clients/go (git subtree split); each module gets
# its tag: vX.Y.Z, zap/vX.Y.Z, zerolog/vX.Y.Z, logrus/vX.Y.Z. A version on
# proxy.golang.org cannot be removed, only retracted.
set -euo pipefail

version="${1:?usage: release.sh vX.Y.Z}"
[[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "the version must look like v1.2.3" >&2; exit 2; }
mirror="${GO_MIRROR:-git@github.com:dlogify/go.git}"
modules=(zap zerolog logrus)

cd "$(git rev-parse --show-toplevel)"
[[ "$(git rev-parse --abbrev-ref HEAD)" == main ]] || { echo "release from main" >&2; exit 1; }
[[ -z "$(git status --porcelain)" ]] || { echo "the working tree is not clean" >&2; exit 1; }
grep -q "const Version = \"${version#v}\"" clients/go/internal/version/version.go ||
  { echo "clients/go/internal/version/version.go does not say ${version#v}" >&2; exit 1; }

(cd clients/go && go vet ./... && go test -race ./...)
for m in "${modules[@]}"; do (cd "clients/go/$m" && go vet ./... && go test -race ./...); done

# The adapters require the core version released with them. Their replace
# directive stays for development; it is ignored by everyone who imports them.
for m in "${modules[@]}"; do (cd "clients/go/$m" && go mod edit -require="dlogify.com/sdk/go@$version"); done
if [[ -n "$(git status --porcelain clients/go)" ]]; then
  git add clients/go/*/go.mod
  git commit -m "chore(go-sdk): adapters require dlogify.com/sdk/go $version"
fi

split="$(git subtree split --prefix clients/go HEAD)"
git push "$mirror" "$split:refs/heads/main"
for tag in "$version" "${modules[@]/%//$version}"; do
  git push "$mirror" "$split:refs/tags/$tag"
done
echo "pushed $split as $version"

check="$(mktemp -d)"
for _ in $(seq 1 30); do
  if (cd "$check" && GOPROXY=https://proxy.golang.org go list -m "dlogify.com/sdk/go@$version" >/dev/null 2>&1); then
    echo "dlogify.com/sdk/go@$version is on proxy.golang.org"
    exit 0
  fi
  sleep 10
done
echo "proxy.golang.org does not list $version yet; check https://pkg.go.dev/dlogify.com/sdk/go" >&2
exit 1
