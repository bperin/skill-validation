#!/usr/bin/env bash
set -euo pipefail

INFRA_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for script in "${INFRA_DIR}"/*.sh; do
  bash -n "${script}"
done

if command -v shellcheck >/dev/null 2>&1; then
  shellcheck "${INFRA_DIR}"/*.sh
fi

require_text() {
  local file="$1"
  local text="$2"
  grep -Fq -- "${text}" "${file}" || {
    echo "${file} is missing required deployment control: ${text}" >&2
    exit 1
  }
}

require_text "${INFRA_DIR}/Dockerfile" './cmd/project-validation'
require_text "${INFRA_DIR}/Dockerfile" 'USER nonroot:nonroot'
require_text "${INFRA_DIR}/cloudbuild.yaml" '${_IMAGE_TAG}'
require_text "${INFRA_DIR}/cloudrun.yaml" '${IMAGE_URI_AT_DIGEST}'
require_text "${INFRA_DIR}/cloudrun.yaml" 'secretKeyRef:'
require_text "${INFRA_DIR}/gcloud.sh" '--no-allow-unauthenticated'
require_text "${INFRA_DIR}/gcloud.sh" '@sha256:'
require_text "${INFRA_DIR}/gcloud.sh" '--add-cloudsql-instances'
require_text "${INFRA_DIR}/provision.sh" 'ec-sign-p256-sha256'
require_text "${INFRA_DIR}/provision.sh" 'ec-sign-secp256k1-sha256'
require_text "${INFRA_DIR}/provision.sh" 'roles/cloudkms.signerVerifier'
require_text "${INFRA_DIR}/provision.sh" '--no-assign-ip'

if grep -Eq -- '--(set|update)-env-vars=.*(DATABASE_URL|ISSUER_TOKEN|EVM_RPC_URL)=' "${INFRA_DIR}/gcloud.sh"; then
  echo "secret material must use Cloud Run secret references" >&2
  exit 1
fi

if grep -Eq -- '--(set|update)-env-vars=.*(^|,)PORT=' "${INFRA_DIR}/gcloud.sh"; then
  echo "PORT is reserved by Cloud Run and must not be set" >&2
  exit 1
fi

echo "deployment assets validated"
