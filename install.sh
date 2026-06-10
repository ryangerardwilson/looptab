#!/usr/bin/env sh
set -eu

module="github.com/ryangerardwilson/looptab/cmd/looptab"

install_from_source() {
  src="$1"
  old_pwd="$(pwd)"
  cd "$src"
  go install ./cmd/looptab
  cd "$old_pwd"
}

install_from_binary() {
  src="$1"
  dest="${HOME}/.local/bin"
  mkdir -p "$dest"
  cp "$src" "$dest/looptab"
  chmod 0755 "$dest/looptab"
}

if [ "$#" -eq 0 ]; then
  go install "${module}@latest"
  exit 0
fi

if [ "$#" -eq 2 ] && [ "$1" = "from" ]; then
  if [ -d "$2" ]; then
    install_from_source "$2"
    exit 0
  fi
  if [ -f "$2" ]; then
    install_from_binary "$2"
    exit 0
  fi
  echo "install source not found: $2" >&2
  exit 1
fi

echo "usage:" >&2
echo "  ./install.sh" >&2
echo "  ./install.sh from <source-checkout-or-binary>" >&2
exit 1

