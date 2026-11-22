#!/usr/bin/env bash
# Download and install the prebuilt PDFium library the backend links against
# (through github.com/klippa-app/go-pdfium) for all PDF work.
#
# The binaries are bblanchon/pdfium-binaries releases: Google's PDFium built as
# one shared library per platform. PDFIUM_RELEASE and the checksums below are
# the single source of truth for the pin; the Dockerfile's pdfium stage and
# developers running this by hand both read them from here. PDFium gets no
# distribution security updates this way, so bump the release from time to
# time: take the new tag from the releases page and the four checksums from its
# assets.
#
# Installs DIR/lib/libpdfium.{so,dylib}, DIR/include, DIR/lib/pkgconfig/pdfium.pc
# (what cgo finds it through), DIR/share/doc/pdfium (licenses) and
# DIR/lib/pdfium.ref, which records the release so a rerun can skip.
#
#   scripts/pdfium-fetch.sh --prefix /usr/local
#   scripts/pdfium-fetch.sh --prefix "$HOME/.local/pdfium"
#   scripts/pdfium-fetch.sh --prefix /opt/pdfium --target-arch arm64
#
# Requires: curl, tar and sha256sum (or shasum on macOS).
set -euo pipefail

PDFIUM_RELEASE="chromium/8165"
pdfium_sha256() {
    case "$1" in
        linux-x64) echo ba7865e16c0271daab75256790be06c26272190afe6e2a70eae0bb39bb99089c ;;
        linux-arm64) echo 338f5dcc907a20331812ae8ab01b16ff18966f4a247d790c8ad8fb49029d768b ;;
        mac-x64) echo 07285915b564e46a432a9909dbc1f15892885ca6489e3faeefc70dee77ed371a ;;
        mac-arm64) echo 112e74d717f3e0310273b70f3c8780e8a61ff11b34834124465c8305c841d350 ;;
    esac
}

PREFIX=""
TARGET_ARCH=""
FORCE=0

usage() {
    cat <<EOF
Usage: $(basename "$0") --prefix DIR [--target-arch amd64|arm64] [--force]

  --prefix DIR       install root; the library lands in DIR/lib, headers in DIR/include
  --target-arch A    amd64 or arm64 (default: this machine's)
  --force            download again even when DIR/lib/pdfium.ref already names this release

Pinned PDFium release: $PDFIUM_RELEASE
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --prefix) PREFIX="${2:?--prefix needs a directory}"; shift 2 ;;
        --target-arch) TARGET_ARCH="${2:?--target-arch needs amd64 or arm64}"; shift 2 ;;
        --force) FORCE=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
    esac
done
[[ -n "$PREFIX" ]] || { usage >&2; exit 2; }

case "$(uname -s)" in
    Linux) os=linux; libname=libpdfium.so ;;
    Darwin) os=mac; libname=libpdfium.dylib ;;
    *) echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac
if [[ -z "$TARGET_ARCH" ]]; then
    case "$(uname -m)" in
        x86_64 | amd64) TARGET_ARCH=amd64 ;;
        aarch64 | arm64) TARGET_ARCH=arm64 ;;
        *) echo "unsupported machine: $(uname -m)" >&2; exit 1 ;;
    esac
fi
case "$TARGET_ARCH" in
    amd64) platform="$os-x64" ;;
    arm64) platform="$os-arm64" ;;
    *) echo "unsupported --target-arch: $TARGET_ARCH" >&2; exit 2 ;;
esac

if [[ $FORCE -eq 0 && -f "$PREFIX/lib/pdfium.ref" && "$(cat "$PREFIX/lib/pdfium.ref")" == "$PDFIUM_RELEASE $platform" ]]; then
    echo "pdfium $PDFIUM_RELEASE ($platform) already in $PREFIX"
    exit 0
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
archive="$work/pdfium.tgz"
curl -fsSL --retry 3 -o "$archive" \
    "https://github.com/bblanchon/pdfium-binaries/releases/download/$PDFIUM_RELEASE/pdfium-$platform.tgz"

if command -v sha256sum >/dev/null; then
    actual="$(sha256sum "$archive" | cut -d' ' -f1)"
else
    actual="$(shasum -a 256 "$archive" | cut -d' ' -f1)"
fi
expected="$(pdfium_sha256 "$platform")"
if [[ "$actual" != "$expected" ]]; then
    echo "pdfium-$platform.tgz: checksum $actual, expected $expected" >&2
    exit 1
fi

mkdir -p "$work/x" && tar -xzf "$archive" -C "$work/x"
mkdir -p "$PREFIX/lib/pkgconfig" "$PREFIX/include" "$PREFIX/share/doc/pdfium"
cp "$work/x/lib/$libname" "$PREFIX/lib/"
cp -R "$work/x/include/." "$PREFIX/include/"
cp -R "$work/x/LICENSE" "$work/x/licenses" "$PREFIX/share/doc/pdfium/"
cat >"$PREFIX/lib/pkgconfig/pdfium.pc" <<EOF
prefix=$PREFIX
libdir=\${prefix}/lib
includedir=\${prefix}/include

Name: PDFium
Description: PDFium ($PDFIUM_RELEASE, bblanchon/pdfium-binaries)
Version: ${PDFIUM_RELEASE#chromium/}
Libs: -L\${libdir} -lpdfium
Cflags: -I\${includedir}
EOF
printf '%s %s\n' "$PDFIUM_RELEASE" "$platform" >"$PREFIX/lib/pdfium.ref"
echo "pdfium $PDFIUM_RELEASE ($platform) installed in $PREFIX"
