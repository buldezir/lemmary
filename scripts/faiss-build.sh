#!/usr/bin/env bash
# Build and install the FAISS C API that bleve's vector search links against.
#
# bleve reaches FAISS through github.com/blevesearch/go-faiss, which is written
# against *blevesearch's fork* of FAISS, not upstream: the fork adds the
# `*_c_ex.h` C entry points that go-faiss calls, so a distribution libfaiss
# package cannot satisfy it however new it is.
#
# FAISS_REF below is the single source of truth for the pin. It is the commit
# named for our bleve version in bleve's own docs/vectors.md compatibility
# table (the same commit covers v2.5.5 through v2.5.7). BUMP IT ONLY TOGETHER
# WITH A BLEVE BUMP, taking the new value from that table -- a mismatched pair
# either fails to link or, worse, links and reads indexes wrongly.
#
# Everything that needs FAISS reads the pin from here: the Dockerfile's faiss
# stage, CI, and developers running it by hand. $PREFIX/lib/faiss.ref records
# what was installed so those callers can skip a rebuild that would change
# nothing.
#
#   scripts/faiss-build.sh --prefix /usr/local
#   scripts/faiss-build.sh --prefix "$HOME/.local/faiss"
#   scripts/faiss-build.sh --prefix /opt/faiss --target-arch arm64
#
# Requires: git, cmake (>= 3.24), ninja, a C++17 compiler and OpenBLAS headers.
# On Debian/Ubuntu: apt install git cmake ninja-build g++ libopenblas-dev
set -euo pipefail

FAISS_REPO="https://github.com/blevesearch/faiss.git"
FAISS_REF="8a59a0c552fa2d14fa871f6b6bc793de1d277f5e"

PREFIX=""
TARGET_ARCH=""
JOBS=""
FORCE=0

usage() {
    cat <<EOF
Usage: $(basename "$0") --prefix DIR [--target-arch amd64|arm64] [--jobs N] [--force]

  --prefix DIR       install root; libraries land in DIR/lib, headers in DIR/include
  --target-arch A    amd64 or arm64; cross-compiles when it differs from the host
  --jobs N           parallel compile jobs (default: all cores)
  --force            rebuild even when DIR/lib/faiss.ref already names this commit

Pinned FAISS commit: $FAISS_REF
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --prefix) PREFIX="${2:?--prefix needs a directory}"; shift 2 ;;
        --target-arch) TARGET_ARCH="${2:?--target-arch needs amd64 or arm64}"; shift 2 ;;
        --jobs) JOBS="${2:?--jobs needs a number}"; shift 2 ;;
        --force) FORCE=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
    esac
done

[[ -n "$PREFIX" ]] || { echo "--prefix is required" >&2; usage >&2; exit 2; }

OS="$(uname -s)"

host_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo amd64 ;;
        aarch64|arm64) echo arm64 ;;
        *) echo "unsupported host architecture: $(uname -m)" >&2; exit 1 ;;
    esac
}

HOST_ARCH="$(host_arch)"
TARGET_ARCH="${TARGET_ARCH:-$HOST_ARCH}"
case "$TARGET_ARCH" in
    amd64|arm64) ;;
    *) echo "--target-arch must be amd64 or arm64, got: $TARGET_ARCH" >&2; exit 2 ;;
esac

if [[ -z "$JOBS" ]]; then
    if [[ "$OS" == "Darwin" ]]; then JOBS="$(sysctl -n hw.ncpu)"; else JOBS="$(nproc)"; fi
fi

# mkdir before realpath: the prefix usually does not exist yet, and the ref file
# below has to be written to the same absolute path cmake installed into.
mkdir -p "$PREFIX"
PREFIX="$(cd "$PREFIX" && pwd)"
REF_FILE="$PREFIX/lib/faiss.ref"

if [[ $FORCE -eq 0 && -f "$REF_FILE" && "$(cat "$REF_FILE")" == "$FAISS_REF" ]]; then
    echo "FAISS $FAISS_REF already installed in $PREFIX (--force to rebuild)"
    exit 0
fi

need() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "missing required tool: $1" >&2
        echo "on Debian/Ubuntu: apt-get install -y git cmake ninja-build g++ libopenblas-dev" >&2
        exit 1
    }
}
need git
need cmake
need ninja

CROSS=0
if [[ "$TARGET_ARCH" != "$HOST_ARCH" ]]; then
    CROSS=1
    [[ "$OS" == "Linux" ]] || { echo "cross-compiling is only wired up for Linux hosts" >&2; exit 1; }
    [[ "$TARGET_ARCH" == "arm64" ]] || { echo "only arm64 cross-compilation is supported" >&2; exit 1; }
    need aarch64-linux-gnu-gcc
    need aarch64-linux-gnu-g++
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
SRC="$WORK/faiss"
BUILD="$WORK/build"

echo "==> Fetching blevesearch/faiss @ $FAISS_REF"
mkdir -p "$SRC"
git -C "$SRC" init -q
git -C "$SRC" remote add origin "$FAISS_REPO"
# Fetching the commit directly, not a branch: the pin is what matters, and a
# depth-1 fetch of one commit is a few seconds against a repository that is
# hundreds of megabytes in full.
git -C "$SRC" fetch -q --depth 1 origin "$FAISS_REF"
git -C "$SRC" checkout -q FETCH_HEAD

CMAKE_ARGS=(
    -G Ninja
    -S "$SRC"
    -B "$BUILD"
    -DCMAKE_BUILD_TYPE=Release
    -DCMAKE_INSTALL_PREFIX="$PREFIX"
    # Shared, because the runtime image ships the two .so files rather than a
    # statically linked binary: FAISS pulls in OpenMP and BLAS, which are far
    # better taken from the distribution.
    -DBUILD_SHARED_LIBS=ON
    -DFAISS_ENABLE_C_API=ON
    -DFAISS_ENABLE_GPU=OFF
    -DFAISS_ENABLE_PYTHON=OFF
    -DFAISS_ENABLE_EXTRAS=OFF
    -DBUILD_TESTING=OFF
    # generic: no AVX2/AVX512/SVE dispatch. The image is published for two
    # architectures and runs on whatever the operator has, so a build that
    # requires instructions the host lacks is a crash, not a slow search.
    -DFAISS_OPT_LEVEL=generic
    # OpenBLAS both ways: BLA_VENDOR picks it over any other BLAS on the
    # machine, and MKL off stops cmake preferring an Intel library that the
    # runtime image does not ship.
    -DBLA_VENDOR=OpenBLAS
    -DFAISS_ENABLE_MKL=OFF
)

if [[ $CROSS -eq 1 ]]; then
    echo "==> Cross-compiling for $TARGET_ARCH"
    # Debian multiarch: the arm64 OpenBLAS lives in /usr/lib/aarch64-linux-gnu
    # next to the host's own copy, so cmake is told the target triplet and kept
    # from resolving libraries anywhere else.
    cat >"$WORK/toolchain.cmake" <<'EOF'
set(CMAKE_SYSTEM_NAME Linux)
set(CMAKE_SYSTEM_PROCESSOR aarch64)
set(CMAKE_C_COMPILER aarch64-linux-gnu-gcc)
set(CMAKE_CXX_COMPILER aarch64-linux-gnu-g++)
set(CMAKE_LIBRARY_ARCHITECTURE aarch64-linux-gnu)
set(CMAKE_FIND_ROOT_PATH /usr/aarch64-linux-gnu /usr/lib/aarch64-linux-gnu)
set(CMAKE_FIND_ROOT_PATH_MODE_PROGRAM NEVER)
set(CMAKE_FIND_ROOT_PATH_MODE_LIBRARY ONLY)
set(CMAKE_FIND_ROOT_PATH_MODE_INCLUDE ONLY)
set(CMAKE_FIND_ROOT_PATH_MODE_PACKAGE ONLY)
EOF
    CMAKE_ARGS+=(-DCMAKE_TOOLCHAIN_FILE="$WORK/toolchain.cmake")
fi

if [[ "$OS" == "Darwin" ]]; then
    # Apple's clang has no OpenMP runtime of its own; go-faiss's README points
    # at Homebrew's libomp, so hand cmake the prefix it is installed under.
    if command -v brew >/dev/null 2>&1 && brew --prefix libomp >/dev/null 2>&1; then
        CMAKE_ARGS+=(-DCMAKE_PREFIX_PATH="$(brew --prefix libomp)")
    fi
fi

echo "==> Configuring"
cmake "${CMAKE_ARGS[@]}"

echo "==> Building (jobs: $JOBS)"
cmake --build "$BUILD" --target faiss faiss_c -- -j "$JOBS"

echo "==> Installing into $PREFIX"
cmake --install "$BUILD"

LIBEXT="so"
[[ "$OS" == "Darwin" ]] && LIBEXT="dylib"

# The install rules above cover libfaiss, libfaiss_c and the headers including
# faiss/c_api/**. They are checked rather than trusted: an upstream that stops
# installing part of the C API would otherwise leave a prefix that only fails
# much later, at the first cgo link.
for lib in "libfaiss.$LIBEXT" "libfaiss_c.$LIBEXT"; do
    if [[ ! -e "$PREFIX/lib/$lib" ]]; then
        found="$(find "$BUILD" -name "$lib" -print -quit || true)"
        [[ -n "$found" ]] || { echo "build produced no $lib" >&2; exit 1; }
        echo "    install rules skipped $lib; copying it from the build tree"
        mkdir -p "$PREFIX/lib"
        cp -a "$(dirname "$found")/$lib"* "$PREFIX/lib/"
    fi
done

if [[ ! -f "$PREFIX/include/faiss/c_api/faiss_c.h" ]]; then
    echo "    install rules skipped the C API headers; copying them from the source tree"
    mkdir -p "$PREFIX/include/faiss/c_api"
    (cd "$SRC/c_api" && find . -name '*.h' -exec cp --parents {} "$PREFIX/include/faiss/c_api/" \;)
fi

# go-faiss calls the fork's extension entry points; their absence is the exact
# symptom of building upstream FAISS instead of blevesearch's, so it is worth a
# named check rather than a link error three commands later.
for header in faiss_c.h Index_c_ex.h index_io_c_ex.h IndexIVF_c_ex.h; do
    [[ -f "$PREFIX/include/faiss/c_api/$header" ]] || {
        echo "missing header $PREFIX/include/faiss/c_api/$header -- wrong FAISS source?" >&2
        exit 1
    }
done

mkdir -p "$PREFIX/lib"
printf '%s\n' "$FAISS_REF" >"$REF_FILE"

echo ""
echo "FAISS $FAISS_REF installed:"
echo "  libraries: $PREFIX/lib"
echo "  headers:   $PREFIX/include"
if [[ "$PREFIX" != "/usr/local" && "$PREFIX" != "/usr" ]]; then
    echo ""
    echo "Build and test the backend against it with:"
    echo "  export CGO_CFLAGS=-I$PREFIX/include"
    echo "  export CGO_LDFLAGS=-L$PREFIX/lib"
    echo "  export LD_LIBRARY_PATH=$PREFIX/lib\${LD_LIBRARY_PATH:+:\$LD_LIBRARY_PATH}"
fi
