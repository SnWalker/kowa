#!/usr/bin/env bash
# Keep child processes on the project toolchain even when shell PATH is reordered.
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$(mise where node)/bin:$(mise where pnpm):$PATH"
exec "$@"
