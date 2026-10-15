#!/usr/bin/env bash
# Extracts a public Ghana PMTiles basemap for LocationField.
# Requires go-pmtiles and the AWS CLI.
# Usage: STORAGE_ENDPOINT=... STORAGE_BUCKET=... ./scripts/generate-ghana-pmtiles.sh

set -euo pipefail

# Ghana's bounding box, with a small buffer beyond the strict border so the
# map doesn't cut off abruptly at the edge.
BBOX="-3.5,4.3,1.3,11.2"
MAXZOOM=14 # Street-level detail. z15/z16 extract to the same size as z15
           # (the source's practical max detail for this region) at 2.4x
           # the file size — not worth it for a form-field location picker.

if [ -z "${SOURCE_URL:-}" ]; then
  # Sorted explicitly by key (a YYYYMMDD-prefixed filename, so lexical order
  # is chronological) rather than trusting builds.json's own array order.
  LATEST_KEY=$(curl -sL "https://build-metadata.protomaps.dev/builds.json" | python3 -c "import json,sys; print(sorted(json.load(sys.stdin), key=lambda b: b['key'])[-1]['key'])")
  SOURCE_URL="https://build.protomaps.com/${LATEST_KEY}"
fi
STORAGE_ENDPOINT="${STORAGE_ENDPOINT:-http://localhost:8334}" # SeaweedFS S3 gateway, dev default.
STORAGE_BUCKET="${STORAGE_BUCKET:-goerp-static-assets}"
REMOTE_KEY="${REMOTE_KEY:-maps/ghana.pmtiles}"
OUTPUT="${OUTPUT:-/tmp/ghana.pmtiles}"

if [[ "$STORAGE_ENDPOINT" == "http://localhost:8334" || "$STORAGE_ENDPOINT" == "http://127.0.0.1:8334" ]]; then
  export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-goerp-dev}"
  export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-goerp-dev-secret}"
  export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-us-east-1}"
fi

echo "Extracting Ghana region (bbox=${BBOX}, maxzoom=${MAXZOOM}) from ${SOURCE_URL}..."
go-pmtiles extract "$SOURCE_URL" "$OUTPUT" --bbox="$BBOX" --maxzoom="$MAXZOOM"

echo "Verifying archive integrity..."
go-pmtiles verify "$OUTPUT"

echo "Uploading to s3://${STORAGE_BUCKET}/${REMOTE_KEY} (${STORAGE_ENDPOINT})..."
aws --endpoint-url "$STORAGE_ENDPOINT" s3 cp "$OUTPUT" "s3://${STORAGE_BUCKET}/${REMOTE_KEY}" --acl public-read
rm -f "$OUTPUT"

echo "Done. Public object URL: ${STORAGE_ENDPOINT}/${STORAGE_BUCKET}/${REMOTE_KEY}"
