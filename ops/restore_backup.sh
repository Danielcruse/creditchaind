#!/usr/bin/env bash
set -euo pipefail

# Downloads and decrypts one backup snapshot into a target directory
# for restore/verification. Run this on the operator's own machine
# (wherever the matching GPG private key actually lives), never on a
# production node - a production node only ever holds the public key.
#
# Usage: restore_backup.sh <s3-uri> <output-dir>
#   e.g. restore_backup.sh s3://my-bucket/node1/creditchain-node1-20260705T120000Z.tar.gz.gpg ./restore-test

S3_URI="${1:?usage: restore_backup.sh <s3-uri> <output-dir>}"
OUT_DIR="${2:?usage: restore_backup.sh <s3-uri> <output-dir>}"
mkdir -p "$OUT_DIR"

WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT

aws s3 cp "$S3_URI" "$WORKDIR/backup.tar.gz.gpg" --only-show-errors
gpg --batch --yes --output "$WORKDIR/backup.tar.gz" --decrypt "$WORKDIR/backup.tar.gz.gpg"
tar -xzf "$WORKDIR/backup.tar.gz" -C "$OUT_DIR"

echo "Restored to $OUT_DIR - point DATA_DIR there and run the node to verify it loads cleanly."
