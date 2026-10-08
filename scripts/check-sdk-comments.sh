#!/usr/bin/env bash
# Fails when a Go comment under sdk/go cites a platform design document,
# an issue number or an engine-internal path. Module authors read SDK
# comments through godoc without access to any of those, so SDK comments
# state behavior directly.
#
# Usage: ./scripts/check-sdk-comments.sh

set -euo pipefail

pattern='^[[:space:]]*//.*([A-Za-z0-9_-]+\.md\b|§|goerp#[0-9]|[Ii]ssue #[0-9]|\(#[0-9]+\)|internal/(engine|module|cli)/)'

if matches="$(git ls-files 'sdk/go/*.go' ':!:sdk/go/**/testdata/**' | xargs grep -nE "$pattern")"; then
	echo "SDK comments must be self-contained; remove references to design docs, issues or engine internals:"
	echo "$matches"
	exit 1
fi
