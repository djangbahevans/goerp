#!/usr/bin/env bash
# Extracts a Ghana-region PMTiles basemap archive from Protomaps' daily
# basemap build and uploads it to this deployment's S3-compatible object
# storage as a public object — the archive goerp#834 points LocationField's
# tileUrl at (docs/components/location-field.md, nexus-docs).
#
# Re-run this whenever the archive needs refreshing (Protomaps' build
# updates daily; there's no reason to re-run on every one, just when the
# current archive feels stale). Requires the go-pmtiles CLI
# (`go install github.com/protomaps/go-pmtiles@latest`) and the AWS CLI.
#
# Usage: STORAGE_ENDPOINT=... STORAGE_BUCKET=... ./scripts/generate-ghana-pmtiles.sh

set -euo pipefail

# Ghana's bounding box, with a small buffer beyond the strict border so the
# map doesn't cut off abruptly at the edge.
BBOX="-3.5,4.3,1.3,11.2"
MAXZOOM=14 # Street-level detail. z15/z16 extract to the same size as z15
           # (the source's practical max detail for this region) at 2.4x
           # the file size — not worth it for a form-field location picker.

if [ -z "${SOURCE_URL:-}" ]; then
  LATEST_KEY=$(curl -sL "https://build-metadata.protomaps.dev/builds.json" | python3 -c "import json,sys; print(json.load(sys.stdin)[-1]['key'])")
  SOURCE_URL="https://build.protomaps.com/${LATEST_KEY}"
fi
STORAGE_ENDPOINT="${STORAGE_ENDPOINT:-http://localhost:8334}" # SeaweedFS S3 gateway, dev default.
STORAGE_BUCKET="${STORAGE_BUCKET:-goerp-static-assets}"
REMOTE_KEY="${REMOTE_KEY:-maps/ghana.pmtiles}"
OUTPUT="${OUTPUT:-/tmp/ghana.pmtiles}"

echo "Extracting Ghana region (bbox=${BBOX}, maxzoom=${MAXZOOM}) from ${SOURCE_URL}..."
go-pmtiles extract "$SOURCE_URL" "$OUTPUT" --bbox="$BBOX" --maxzoom="$MAXZOOM"

echo "Verifying archive integrity..."
go-pmtiles verify "$OUTPUT"

echo "Uploading to s3://${STORAGE_BUCKET}/${REMOTE_KEY} (${STORAGE_ENDPOINT})..."
aws --endpoint-url "$STORAGE_ENDPOINT" s3 cp "$OUTPUT" "s3://${STORAGE_BUCKET}/${REMOTE_KEY}"

echo "Done. Public URL (dev SeaweedFS, no auth): ${STORAGE_ENDPOINT}/${STORAGE_BUCKET}/${REMOTE_KEY}"
echo "A production deployment's storage backend needs its own public-read bucket policy configured separately — this dev instance allows anonymous access by default."
