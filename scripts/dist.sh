#!/usr/bin/env bash
#
# Сборка релизных архивов LedgerFox для поддерживаемых платформ.
# Архивы складываются в bin/ — единственный каталог сборочных артефактов.
#
#   scripts/dist.sh                       # все платформы, версия из git
#   VERSION=v1.0.0 scripts/dist.sh        # фиксированная версия
#
# Внимание: драйвер SQLite требует CGO. Скрипт форсирует CGO_ENABLED=1,
# поэтому кросс-сборка под чужие платформы (linux/windows на macOS) без
# C-кросс-компилятора (например, zig cc) упадёт сразу и с понятной
# ошибкой — вместо молчаливой сборки бинарника с неработающим SQLite.
# Нативная платформа собирается без дополнительных требований.

set -euo pipefail

cd "$(dirname "$0")/.."

: "${NAME:=$(head -1 go.mod | cut -d ' ' -f2 | cut -d '/' -f3)}"
: "${VERSION:=$(git symbolic-ref -q --short HEAD || git describe --tags --exact-match || echo unknown)}"

OUT=bin
mkdir -p "$OUT"

# Промежуточная сборка идёт во временный каталог: bin/ не очищается,
# и готовые архивы соседних платформ не затираются.
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# Матрицу можно сузить, например только нативная платформа:
#   GOOSES=darwin GOARCHES=arm64 scripts/dist.sh
: "${GOOSES:=windows linux darwin}"
: "${GOARCHES:=amd64 arm64}"

for GOOS in $GOOSES; do
  for GOARCH in $GOARCHES; do
    DIST="$NAME-$VERSION.$GOOS.$GOARCH"
    PKG="$WORK/$DIST"
    mkdir -p "$PKG"

    for CMD in cmd/*; do
      BINARY=$(basename "$CMD")
      if [ "$GOOS" = "windows" ]; then
        BINARY="$BINARY.exe"
      fi

      GOOS=$GOOS GOARCH=$GOARCH CGO_ENABLED=1 go build -o "$PKG/$BINARY" "./$CMD"
    done

    cp README.md LICENSE "$PKG/"
    tar -czf "$OUT/$DIST.tar.gz" -C "$WORK" "$DIST"
    echo "$OUT/$DIST.tar.gz"
  done
done
