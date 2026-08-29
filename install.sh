#!/bin/sh
set -eu

repository=${MESIJ_REPOSITORY:-withakay/mesij}
install_dir=${MESIJ_INSTALL_DIR:-"${HOME}/.local/bin"}
requested_version=${MESIJ_VERSION:-latest}

die() {
  printf 'mesij installer: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Install mesij from a GitHub release.

Options:
  --dir DIR       Install into DIR (default: $HOME/.local/bin)
  --version VER   Install a release version, with or without the v prefix
  -h, --help      Show this help

Environment:
  MESIJ_INSTALL_DIR   Same as --dir
  MESIJ_VERSION       Same as --version
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --dir)
      [ "$#" -ge 2 ] || die "--dir needs a value"
      install_dir=$2
      shift 2
      ;;
    --version)
      [ "$#" -ge 2 ] || die "--version needs a value"
      requested_version=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

case "$requested_version" in
  latest)
    release_json=$(curl -fsSL \
      -H 'Accept: application/vnd.github+json' \
      "https://api.github.com/repos/$repository/releases/latest") || \
      die "could not query the latest release"
    tag=$(printf '%s\n' "$release_json" | \
      awk -F '"' '$2 == "tag_name" { print $4; exit }')
    [ -n "$tag" ] || die "latest release did not include a tag"
    ;;
  v*)
    tag=$requested_version
    ;;
  *)
    tag=v$requested_version
    ;;
esac

version=${tag#v}
case "$version" in
  ''|*[!0-9A-Za-z._-]*)
    die "invalid version: $version"
    ;;
esac

os=$(uname -s)
case "$os" in
  Darwin)
    os=darwin
    ;;
  Linux)
    os=linux
    ;;
  *)
    die "unsupported operating system: $os"
    ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64)
    arch=amd64
    ;;
  arm64|aarch64)
    arch=arm64
    ;;
  *)
    die "unsupported architecture: $arch"
    ;;
esac

asset="mesij_${version}_${os}_${arch}.tar.gz"
base_url="https://github.com/$repository/releases/download/$tag"
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/mesij-install.XXXXXX")

cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT HUP INT TERM

curl -fsSL "$base_url/$asset" -o "$tmp_dir/$asset" || \
  die "could not download $asset"
curl -fsSL "$base_url/checksums.txt" -o "$tmp_dir/checksums.txt" || \
  die "could not download release checksums"

expected=$(awk -v asset="$asset" '$2 == asset { print $1; exit }' \
  "$tmp_dir/checksums.txt")
[ -n "$expected" ] || die "release checksums do not include $asset"

if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp_dir/$asset" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp_dir/$asset" | awk '{ print $1 }')
else
  die "sha256sum or shasum is required to verify the download"
fi

[ "$actual" = "$expected" ] || die "checksum verification failed"

tar -xzf "$tmp_dir/$asset" -C "$tmp_dir"
[ -f "$tmp_dir/mesij" ] || die "release archive did not contain mesij"

mkdir -p "$install_dir"
install -m 0755 "$tmp_dir/mesij" "$install_dir/mesij"
printf 'Installed mesij %s to %s/mesij\n' "$version" "$install_dir"

case ":${PATH}:" in
  *:"$install_dir":*)
    ;;
  *)
    printf 'Add %s to PATH if mesij is not found.\n' "$install_dir"
    ;;
esac
