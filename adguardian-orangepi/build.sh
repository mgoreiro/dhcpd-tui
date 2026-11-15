#!/usr/bin/env bash
# Compila AdGuardian-Term para Orange Pi Zero 2 (Allwinner H616, Cortex-A53, aarch64)
# Requiere Docker. Uso: ./build.sh
set -euo pipefail
cd "$(dirname "$0")"
[ -d src ] || git clone --depth 1 https://github.com/Lissy93/adguardian-term src
docker run --rm --platform linux/arm64 -v "$PWD/src":/work -w /work \
  -e RUSTFLAGS="-C target-cpu=cortex-a53" rust:1-bookworm bash -c \
  'apt-get update -qq && apt-get install -y -qq cmake >/dev/null && cargo build --release --target-dir /tmp/t && cp /tmp/t/release/adguardian /work/adguardian && strip /work/adguardian'
mv src/adguardian ./adguardian
