#!/usr/bin/env bash
set -euo pipefail

# Snapshots this node's chain state (state.json + blockchain.json),
# encrypts the snapshot to the operator's own GPG public key (this
# script never sees or stores the matching private key - only the
# public key ever lives on a production server), and uploads it to a
# versioned S3 bucket. Meant to run on a schedule via
# creditchain-backup.timer, not by hand.
#
# Required environment (see creditchain-backup.env.example):
#   BACKUP_DATA_DIR      - directory containing state.json/blockchain.json
#   BACKUP_S3_BUCKET     - target S3 bucket name
#   BACKUP_S3_PREFIX     - key prefix within the bucket (e.g. node1, node2)
#   BACKUP_GPG_RECIPIENT - operator's GPG key ID/fingerprint to encrypt to
#                          (must already be imported: gpg --import operator_pubkey.asc)
#   AWS credentials      - standard AWS CLI env vars (AWS_ACCESS_KEY_ID /
#                          AWS_SECRET_ACCESS_KEY / AWS_DEFAULT_REGION),
#                          scoped to write-only on the target bucket

: "${BACKUP_DATA_DIR:?BACKUP_DATA_DIR must be set}"
: "${BACKUP_S3_BUCKET:?BACKUP_S3_BUCKET must be set}"
: "${BACKUP_S3_PREFIX:?BACKUP_S3_PREFIX must be set}"
: "${BACKUP_GPG_RECIPIENT:?BACKUP_GPG_RECIPIENT must be set}"

TIMESTAMP=$(date -u +%Y%m%dT%H%M%SZ)
WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT

ARCHIVE="$WORKDIR/creditchain-${BACKUP_S3_PREFIX}-${TIMESTAMP}.tar.gz"
ENCRYPTED="$ARCHIVE.gpg"

tar -czf "$ARCHIVE" -C "$BACKUP_DATA_DIR" state.json blockchain.json

gpg --batch --yes --trust-model always \
    --recipient "$BACKUP_GPG_RECIPIENT" \
    --output "$ENCRYPTED" \
    --encrypt "$ARCHIVE"

aws s3 cp "$ENCRYPTED" "s3://${BACKUP_S3_BUCKET}/${BACKUP_S3_PREFIX}/$(basename "$ENCRYPTED")" \
    --only-show-errors

echo "Backup uploaded: s3://${BACKUP_S3_BUCKET}/${BACKUP_S3_PREFIX}/$(basename "$ENCRYPTED")"
