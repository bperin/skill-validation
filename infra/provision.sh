#!/usr/bin/env bash
set -euo pipefail

INFRA_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=infra/lib.sh
source "${INFRA_DIR}/lib.sh"

load_config
require_base_config
require_command gcloud

for name in CLOUD_SQL_TIER CLOUD_SQL_AVAILABILITY_TYPE CLOUD_SQL_DISK_SIZE_GB; do
  require_var "${name}"
done

RUNTIME_SA_EMAIL="$(runtime_service_account_email)"
CONNECTION_NAME="$(cloud_sql_connection_name)"

ensure_project_role() {
  local role="$1"
  gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
    --member="serviceAccount:${RUNTIME_SA_EMAIL}" \
    --role="${role}" \
    --condition=None \
    --quiet >/dev/null
}

ensure_secret() {
  local secret_id="$1"
  local value_file="${2:-}"

  if ! gcloud secrets describe "${secret_id}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
    gcloud secrets create "${secret_id}" --project="${PROJECT_ID}" --replication-policy=automatic
  fi

  if [[ -n "${value_file}" ]]; then
    [[ -f "${value_file}" ]] || {
      echo "secret value file does not exist: ${value_file}" >&2
      exit 2
    }
    gcloud secrets versions add "${secret_id}" --project="${PROJECT_ID}" --data-file="${value_file}" >/dev/null
  fi

  gcloud secrets add-iam-policy-binding "${secret_id}" \
    --project="${PROJECT_ID}" \
    --member="serviceAccount:${RUNTIME_SA_EMAIL}" \
    --role=roles/secretmanager.secretAccessor \
    --condition=None \
    --quiet >/dev/null
}

ensure_kms_key() {
  local key_name="$1"
  local algorithm="$2"
  local version_id="$3"

  if ! gcloud kms keys describe "${key_name}" \
    --project="${PROJECT_ID}" --location="${KMS_LOCATION}" \
    --keyring="${KMS_KEY_RING}" >/dev/null 2>&1; then
    gcloud kms keys create "${key_name}" \
      --project="${PROJECT_ID}" --location="${KMS_LOCATION}" \
      --keyring="${KMS_KEY_RING}" --purpose=asymmetric-signing \
      --default-algorithm="${algorithm}" --protection-level=software
  fi

  gcloud kms keys versions describe "${version_id}" \
    --project="${PROJECT_ID}" --location="${KMS_LOCATION}" \
    --keyring="${KMS_KEY_RING}" --key="${key_name}" >/dev/null

  gcloud kms keys add-iam-policy-binding "${key_name}" \
    --project="${PROJECT_ID}" --location="${KMS_LOCATION}" \
    --keyring="${KMS_KEY_RING}" \
    --member="serviceAccount:${RUNTIME_SA_EMAIL}" \
    --role=roles/cloudkms.signerVerifier \
    --condition=None \
    --quiet >/dev/null
}

gcloud services enable \
  artifactregistry.googleapis.com \
  cloudbuild.googleapis.com \
  cloudkms.googleapis.com \
  compute.googleapis.com \
  iam.googleapis.com \
  run.googleapis.com \
  secretmanager.googleapis.com \
  servicenetworking.googleapis.com \
  sqladmin.googleapis.com \
  --project="${PROJECT_ID}"

if ! gcloud artifacts repositories describe "${ARTIFACT_REPOSITORY}" \
  --project="${PROJECT_ID}" --location="${REGION}" >/dev/null 2>&1; then
  gcloud artifacts repositories create "${ARTIFACT_REPOSITORY}" \
    --project="${PROJECT_ID}" --location="${REGION}" \
    --repository-format=docker \
    --description="Immutable images for the COMPANY attestation issuer"
fi

if ! gcloud iam service-accounts describe "${RUNTIME_SA_EMAIL}" \
  --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud iam service-accounts create "${RUNTIME_SERVICE_ACCOUNT%@*}" \
    --project="${PROJECT_ID}" \
    --display-name="COMPANY attestation issuer runtime"
fi

ensure_project_role roles/cloudsql.client
ensure_project_role roles/logging.logWriter
ensure_project_role roles/monitoring.metricWriter

if ! gcloud compute networks describe "${NETWORK_NAME}" --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud compute networks create "${NETWORK_NAME}" --project="${PROJECT_ID}" --subnet-mode=custom
fi

if ! gcloud compute networks subnets describe "${SUBNET_NAME}" \
  --project="${PROJECT_ID}" --region="${REGION}" >/dev/null 2>&1; then
  gcloud compute networks subnets create "${SUBNET_NAME}" \
    --project="${PROJECT_ID}" --region="${REGION}" \
    --network="${NETWORK_NAME}" --range="${SUBNET_RANGE}"
fi

if ! gcloud compute addresses describe "${PRIVATE_SERVICE_RANGE}" \
  --project="${PROJECT_ID}" --global >/dev/null 2>&1; then
  gcloud compute addresses create "${PRIVATE_SERVICE_RANGE}" \
    --project="${PROJECT_ID}" --global --purpose=VPC_PEERING \
    --prefix-length=16 --network="${NETWORK_NAME}"
fi

if ! gcloud services vpc-peerings list \
  --project="${PROJECT_ID}" --network="${NETWORK_NAME}" \
  --service=servicenetworking.googleapis.com \
  --format='value(network)' | grep -q .; then
  gcloud services vpc-peerings connect \
    --project="${PROJECT_ID}" --network="${NETWORK_NAME}" \
    --service=servicenetworking.googleapis.com \
    --ranges="${PRIVATE_SERVICE_RANGE}"
fi

if ! gcloud sql instances describe "${CLOUD_SQL_INSTANCE}" \
  --project="${PROJECT_ID}" >/dev/null 2>&1; then
  gcloud sql instances create "${CLOUD_SQL_INSTANCE}" \
    --project="${PROJECT_ID}" --region="${REGION}" \
    --database-version=POSTGRES_16 --edition=ENTERPRISE \
    --tier="${CLOUD_SQL_TIER}" \
    --availability-type="${CLOUD_SQL_AVAILABILITY_TYPE}" \
    --storage-size="${CLOUD_SQL_DISK_SIZE_GB}" --storage-type=SSD \
    --storage-auto-increase --backup --enable-point-in-time-recovery \
    --network="projects/${PROJECT_ID}/global/networks/${NETWORK_NAME}" \
    --no-assign-ip --deletion-protection
fi

if ! gcloud sql databases describe "${CLOUD_SQL_DATABASE}" \
  --project="${PROJECT_ID}" --instance="${CLOUD_SQL_INSTANCE}" >/dev/null 2>&1; then
  gcloud sql databases create "${CLOUD_SQL_DATABASE}" \
    --project="${PROJECT_ID}" --instance="${CLOUD_SQL_INSTANCE}"
fi

db_user_exists="$(gcloud sql users list --project="${PROJECT_ID}" \
  --instance="${CLOUD_SQL_INSTANCE}" --filter="name=${CLOUD_SQL_USER}" \
  --format='value(name)' | head -n 1)"

if [[ -z "${db_user_exists}" ]]; then
  require_var DATABASE_PASSWORD_FILE
  [[ -f "${DATABASE_PASSWORD_FILE}" ]] || {
    echo "DATABASE_PASSWORD_FILE does not exist: ${DATABASE_PASSWORD_FILE}" >&2
    exit 2
  }
  database_password="$(tr -d '\r\n' < "${DATABASE_PASSWORD_FILE}")"
  gcloud sql users create "${CLOUD_SQL_USER}" --project="${PROJECT_ID}" \
    --instance="${CLOUD_SQL_INSTANCE}" --password="${database_password}"
elif [[ "${ROTATE_DATABASE_CREDENTIALS:-false}" == "true" ]]; then
  require_var DATABASE_PASSWORD_FILE
  database_password="$(tr -d '\r\n' < "${DATABASE_PASSWORD_FILE}")"
  gcloud sql users set-password "${CLOUD_SQL_USER}" --project="${PROJECT_ID}" \
    --instance="${CLOUD_SQL_INSTANCE}" --password="${database_password}"
fi

database_url_file="${DATABASE_URL_FILE:-}"
temporary_database_url=""
if [[ -z "${database_url_file}" && -n "${DATABASE_PASSWORD_FILE:-}" ]]; then
  require_command jq
  database_password="$(tr -d '\r\n' < "${DATABASE_PASSWORD_FILE}")"
  encoded_user="$(jq -rn --arg value "${CLOUD_SQL_USER}" '$value|@uri')"
  encoded_password="$(jq -rn --arg value "${database_password}" '$value|@uri')"
  encoded_database="$(jq -rn --arg value "${CLOUD_SQL_DATABASE}" '$value|@uri')"
  temporary_database_url="$(mktemp)"
  trap 'rm -f "${temporary_database_url}"' EXIT
  printf 'postgres://%s:%s@/%s?host=/cloudsql/%s&sslmode=disable' \
    "${encoded_user}" "${encoded_password}" "${encoded_database}" "${CONNECTION_NAME}" \
    > "${temporary_database_url}"
  database_url_file="${temporary_database_url}"
fi

ensure_secret "${DATABASE_URL_SECRET}" "${database_url_file}"
ensure_secret "${ISSUER_TOKEN_SECRET}" "${ISSUER_TOKEN_FILE:-}"
ensure_secret "${EVM_RPC_URL_SECRET}" "${EVM_RPC_URL_FILE:-}"

if ! gcloud kms keyrings describe "${KMS_KEY_RING}" \
  --project="${PROJECT_ID}" --location="${KMS_LOCATION}" >/dev/null 2>&1; then
  gcloud kms keyrings create "${KMS_KEY_RING}" \
    --project="${PROJECT_ID}" --location="${KMS_LOCATION}"
fi

ensure_kms_key "${ATTESTATION_KMS_KEY}" ec-sign-p256-sha256 "${ATTESTATION_KMS_KEY_VERSION_ID}"
ensure_kms_key "${EVM_PUBLISHER_KMS_KEY}" ec-sign-secp256k1-sha256 "${EVM_PUBLISHER_KMS_KEY_VERSION_ID}"

echo "Provisioning complete for ${PROJECT_ID}/${REGION}."
echo "Runtime service account: ${RUNTIME_SA_EMAIL}"
echo "Cloud SQL connection: ${CONNECTION_NAME} (private IP only)"
echo "No Cloud Run service, verifier contract, or blockchain transaction was created."
