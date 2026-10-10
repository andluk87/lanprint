#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
docker build --target artifact --output type=local,dest=. .
test -s LANPrint.exe
sha256sum --check LANPrint.exe.sha256
printf 'LANPrint.exe saved to %s\n' "$PWD"
