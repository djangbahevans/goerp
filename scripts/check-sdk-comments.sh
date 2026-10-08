#!/usr/bin/env bash
# Checks tracker references in Go comments and self-contained SDK/ABI documentation.
# Usage: ./scripts/check-sdk-comments.sh

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
exec go run ./scripts/check-go-comments
