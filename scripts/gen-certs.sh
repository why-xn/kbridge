#!/bin/bash
# Generate a self-signed TLS certificate for local kbridge development.
#
# Produces a cert/key valid for localhost and 127.0.0.1. The same cert acts as
# its own CA: point central at tls.crt/tls.key and the agent at tls.crt as its
# ca_file. NOT for production use.
set -euo pipefail

OUT_DIR="${1:-certs}"
DAYS="${CERT_DAYS:-365}"

if ! command -v openssl >/dev/null 2>&1; then
    echo "error: openssl is required" >&2
    exit 1
fi

mkdir -p "${OUT_DIR}"

openssl req -x509 -newkey rsa:2048 -nodes \
    -keyout "${OUT_DIR}/tls.key" \
    -out "${OUT_DIR}/tls.crt" \
    -days "${DAYS}" \
    -subj "/CN=localhost" \
    -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"

chmod 600 "${OUT_DIR}/tls.key"

echo "Wrote ${OUT_DIR}/tls.crt and ${OUT_DIR}/tls.key (valid ${DAYS} days)."
echo "Central:  tls.cert_file=${OUT_DIR}/tls.crt  tls.key_file=${OUT_DIR}/tls.key"
echo "Agent:    central.tls.enabled=true  central.tls.ca_file=${OUT_DIR}/tls.crt"
