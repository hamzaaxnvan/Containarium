#!/usr/bin/env bash
# Tests for the air-gapped bundle gates (#3061), against stubbed apt tools:
#   - download-deps.sh downloads exactly the name=version set apt's resolver
#     picks (providers of virtual names included; virtual names themselves
#     used to abort the whole batch), and fails on a resolver error, an
#     apt-get download error, an empty download, and a dependency the bundle
#     cannot satisfy;
#   - build-bundle.sh fails when the cache holds no .deb (opt-out:
#     BUNDLE_ALLOW_EMPTY_APT=1) and ships no images/ directory;
#   - check-bundle-content.sh rejects a tarball without debs or with images/;
#   - the offline installer errors on an empty apt/ unless --skip-apt, and
#     has no base-image import step;
#   - add-zabbly-repo.sh refuses a key with the wrong fingerprint.
# Needs bash, coreutils, tar, gzip, dpkg-deb and gpg (all on ubuntu runners).
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
deps=$repo/scripts/bundle/download-deps.sh
build=$repo/scripts/bundle/build-bundle.sh
content=$repo/scripts/bundle/check-bundle-content.sh
zabbly=$repo/scripts/bundle/add-zabbly-repo.sh
installer=$repo/hacks/offline-install/install.sh
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
fail=0
t() { echo "FAIL: $*"; fail=1; }
pass() { echo "ok:   $*"; }

# ---------------------------------------------------------------- fixtures
# mkdeb NAME [DEPENDS] [PROVIDES] -> $tmp/debs/NAME.deb
mkdir -p "$tmp/debs"
mkdeb() {
  local d=$tmp/debsrc/$1
  mkdir -p "$d/DEBIAN"
  {
    printf 'Package: %s\nVersion: 1.0\nArchitecture: all\n' "$1"
    printf 'Maintainer: test <test@example.com>\nDescription: fixture\n'
    [ -n "${2:-}" ] && printf 'Depends: %s\n' "$2"
    [ -n "${3:-}" ] && printf 'Provides: %s\n' "$3"
  } >"$d/DEBIAN/control"
  dpkg-deb --root-owner-group -Zgzip --build "$d" "$tmp/debs/$1.deb" >/dev/null
}
mkdeb alpha 'libx, virt-thing | libx'
mkdeb beta 'libx (>= 1.0), python3:any'
mkdeb libx '' 'virt-thing, python3'
mkdeb broken 'libmissing (>= 2)'

# Restricted PATH: the stubs plus only the system tools the scripts need, so
# a host apt-get never leaks into a test.
farm=$tmp/farm; mkdir -p "$farm"
for c in bash sh env sort uniq grep awk sed cat mkdir mktemp ls find dirname basename \
         cp mv rm touch tr wc head tail cut dpkg-deb tar gzip printf date; do
  p=$(command -v "$c" || true)
  [ -n "$p" ] && ln -sf "$p" "$farm/$c"
done

stubs=$tmp/stubs; mkdir -p "$stubs"
# apt-get install -s PKGS: what apt's resolver would install on an empty
# system: the union of FAKE/resolve/PKG, as `Inst` lines; an unknown PKG
# fails like apt does. FAKE_RESOLVE_RC makes it exit non-zero AFTER printing
# the Inst lines, so only the exit status shows the failure.
# Every call appends "<mode> <-o values>" to $FAKE/apt-get.opts.
# apt-get download NAME=VER...: records the args; FAKE_APT_RC makes it exit
# non-zero AFTER writing every .deb (a partial-failure shape only the exit
# status reveals); FAKE_NO_WRITE pretends success without writing anything.
cat >"$stubs/apt-get" <<'EOF'
#!/bin/bash
mode=; pkgs=(); opts=()
while [ $# -gt 0 ]; do
  case "$1" in
    -o) shift; opts+=("$1") ;;
    -*) ;;
    install|download) mode=$1 ;;
    *) pkgs+=("$1") ;;
  esac
  shift
done
echo "$mode ${opts[*]}" >>"$FAKE/apt-get.opts"
case "$mode" in
  install)
    echo "install ${pkgs[*]}" >>"$FAKE/apt-get.log"
    for n in "${pkgs[@]}"; do
      [ -f "$FAKE/resolve/$n" ] || { echo "E: Unable to locate package $n" >&2; exit 100; }
    done
    for n in "${pkgs[@]}"; do cat "$FAKE/resolve/$n"; done | sort -u | while read -r p; do
      echo "Inst $p (1.0 Fake:1/stable [all])"
    done
    if [ "${FAKE_RESOLVE_RC:-0}" != 0 ]; then echo "E: Unable to correct problems" >&2; exit "$FAKE_RESOLVE_RC"; fi ;;
  download)
    echo "download ${pkgs[*]}" >>"$FAKE/apt-get.log"
    [ -n "${FAKE_NO_WRITE:-}" ] && exit 0
    for nv in "${pkgs[@]}"; do
      n=${nv%%=*}
      [ -f "$FAKE/debs/$n.deb" ] || { echo "E: Can't find package $n" >&2; exit 100; }
      cp "$FAKE/debs/$n.deb" "./${n}_1.0_all.deb"
    done
    if [ "${FAKE_APT_RC:-0}" != 0 ]; then echo "E: Failed to fetch" >&2; exit "$FAKE_APT_RC"; fi ;;
  *) echo "stub apt-get: unsupported mode" >&2; exit 2 ;;
esac
EOF
chmod +x "$stubs"/*

# newfake DIR: alpha's closure is alpha + libx (libx provides the virtual
# virt-thing and python3 that alpha and beta depend on); broken depends on a
# package its resolved set lacks.
newfake() {
  local f=$1; mkdir -p "$f/resolve" "$f/debs"
  cp "$tmp/debs/"*.deb "$f/debs/"
  printf 'alpha\nlibx\n' >"$f/resolve/alpha"
  printf 'beta\nlibx\n' >"$f/resolve/beta"
  printf 'broken\n' >"$f/resolve/broken"
  : >"$f/apt-get.log"
}

# deps NAME [ENV=VAL...] -> runs download-deps.sh (apt step only) under the
# stubs; output in $tmp/NAME.out, staging in $tmp/NAME.stage.
deps() {
  local name=$1; shift
  newfake "$tmp/$name.fake"
  env -i HOME="$tmp" PATH="$stubs:$farm" FAKE="$tmp/$name.fake" \
    STAGING_DIR="$tmp/$name.stage" DEPS_ONLY=apt APT_PACKAGES="alpha beta" \
    "$@" bash "$deps" >"$tmp/$name.out" 2>&1
}

# ------------------------------------------------- download-deps.sh tests
if deps ok; then
  n=$(find "$tmp/ok.stage/apt" -name '*.deb' | wc -l)
  [ "$n" -eq 3 ] && pass "download-deps: resolved closure downloaded (3 debs)" || t "download-deps: want 3 debs, got $n"
  grep -qx 'download alpha=1.0 beta=1.0 libx=1.0' "$tmp/ok.fake/apt-get.log" \
    && pass "download-deps: downloads exactly the resolver's name=version set" \
    || { t "download-deps: download args differ from the resolved set"; cat "$tmp/ok.fake/apt-get.log"; }
  grep -q 'closure OK' "$tmp/ok.out" && pass "download-deps: virtual deps met through Provides" || t "download-deps: closure check did not run"
else t "download-deps: good closure failed"; cat "$tmp/ok.out"; fi

if deps resolvefail FAKE_RESOLVE_RC=100; then t "download-deps: resolver failure exited 0"
else
  grep -q 'apt could not resolve' "$tmp/resolvefail.out" && pass "download-deps: resolver exit status checked" \
    || t "download-deps: resolver failure not reported as such"; fi

if deps aptfail FAKE_APT_RC=100; then t "download-deps: failing apt-get download exited 0"
else
  grep -q 'apt-get download failed' "$tmp/aptfail.out" && pass "download-deps: apt-get download exit status checked" \
    || t "download-deps: apt-get download failure not reported as such"; fi

