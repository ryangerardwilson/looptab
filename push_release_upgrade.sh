#!/usr/bin/env sh
set -eu

if [ "$#" -ne 1 ]; then
  echo "usage: ./push_release_upgrade.sh <version>" >&2
  echo "example: ./push_release_upgrade.sh v0.1.0" >&2
  exit 1
fi

version="$1"

case "$version" in
  v*) ;;
  *)
    echo "version must start with v, for example v0.1.0" >&2
    exit 1
    ;;
esac

go test ./...
go build -ldflags "-X main.version=${version}" -o "dist/looptab" ./cmd/looptab

git diff --check
git status --short

git tag "$version"
git push origin HEAD
git push origin "$version"

gh release create "$version" "dist/looptab" \
  --title "looptab ${version}" \
  --notes "Release ${version}"

./install.sh from "dist/looptab"
looptab version

