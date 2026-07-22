#!/usr/bin/env bash
set -euo pipefail

INFRA_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_FILE="${CONFIG_FILE:-${INFRA_DIR}/config.env}"

load_config() {
  if [[ ! -f "${CONFIG_FILE}" ]]; then
    echo "missing ${CONFIG_FILE}; copy infra/config.env.example and fill required values" >&2
    exit 2
  fi

  # shellcheck disable=SC1090
  source "${CONFIG_FILE}"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "required command not found: $1" >&2
    exit 2
  }
}

require_var() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "required configuration is empty: ${name}" >&2
    exit 2
  fi
}

runtime_service_account_email() {
  if [[ "${RUNTIME_SERVICE_ACCOUNT}" == *"@"* ]]; then
    printf '%s\n' "${RUNTIME_SERVICE_ACCOUNT}"
  else
    printf '%s@%s.iam.gserviceaccount.com\n' "${RUNTIME_SERVICE_ACCOUNT}" "${PROJECT_ID}"
  fi
}

cloud_sql_connection_name() {
  printf '%s:%s:%s\n' "${PROJECT_ID}" "${REGION}" "${CLOUD_SQL_INSTANCE}"
}

attestation_kms_key_version() {
  printf 'projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s/cryptoKeyVersions/%s\n' \
    "${PROJECT_ID}" "${KMS_LOCATION}" "${KMS_KEY_RING}" "${ATTESTATION_KMS_KEY}" \
    "${ATTESTATION_KMS_KEY_VERSION_ID}"
}

evm_kms_key_version() {
  printf 'projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s/cryptoKeyVersions/%s\n' \
    "${PROJECT_ID}" "${KMS_LOCATION}" "${KMS_KEY_RING}" "${EVM_PUBLISHER_KMS_KEY}" \
    "${EVM_PUBLISHER_KMS_KEY_VERSION_ID}"
}

require_base_config() {
  local name
  for name in PROJECT_ID REGION SERVICE_NAME ARTIFACT_REPOSITORY IMAGE_NAME \
    RUNTIME_SERVICE_ACCOUNT NETWORK_NAME SUBNET_NAME SUBNET_RANGE \
    PRIVATE_SERVICE_RANGE CLOUD_SQL_INSTANCE CLOUD_SQL_DATABASE CLOUD_SQL_USER \
    DATABASE_URL_SECRET ISSUER_TOKEN_SECRET KMS_LOCATION KMS_KEY_RING \
    ATTESTATION_KMS_KEY ATTESTATION_KMS_KEY_VERSION_ID ATTESTATION_ISSUER \
    VERIFICATION_BASE_URL EVM_RPC_URL_SECRET EVM_CHAIN_ID EVM_VERIFIER_CONTRACT \
    EVM_PUBLISHER_ADDRESS EVM_PUBLISHER_KMS_KEY EVM_PUBLISHER_KMS_KEY_VERSION_ID; do
    require_var "${name}"
  done
}
