#!/usr/bin/env bash
set -euo pipefail

INFRA_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=infra/lib.sh
source "${INFRA_DIR}/lib.sh"

usage() {
  echo "usage: $0 deploy [IMAGE@sha256:DIGEST] | smoke | revisions | rollback REVISION" >&2
  exit 2
}

load_config
require_base_config
require_command gcloud

RUNTIME_SA_EMAIL="$(runtime_service_account_email)"
CONNECTION_NAME="$(cloud_sql_connection_name)"
ATTESTATION_KEY_VERSION="$(attestation_kms_key_version)"
EVM_KEY_VERSION="$(evm_kms_key_version)"
COMMAND="${1:-}"

case "${COMMAND}" in
  deploy)
    IMAGE_REF="${2:-}"
    if [[ -z "${IMAGE_REF}" && -f "${INFRA_DIR}/.last-image" ]]; then
      IMAGE_REF="$(<"${INFRA_DIR}/.last-image")"
    fi
    if [[ ! "${IMAGE_REF}" =~ @sha256:[0-9a-f]{64}$ ]]; then
      echo "deploy requires an Artifact Registry image pinned by sha256 digest" >&2
      exit 2
    fi

    gcloud run deploy "${SERVICE_NAME}" \
      --project="${PROJECT_ID}" --region="${REGION}" --platform=managed \
      --image="${IMAGE_REF}" --service-account="${RUNTIME_SA_EMAIL}" \
      --no-allow-unauthenticated --ingress=all \
      --network="${NETWORK_NAME}" --subnet="${SUBNET_NAME}" \
      --vpc-egress=private-ranges-only \
      --add-cloudsql-instances="${CONNECTION_NAME}" \
      --set-secrets="DATABASE_URL=${DATABASE_URL_SECRET}:latest,ISSUER_TOKEN=${ISSUER_TOKEN_SECRET}:latest,EVM_RPC_URL=${EVM_RPC_URL_SECRET}:latest" \
      --set-env-vars="GCP_PROJECT_ID=${PROJECT_ID},ATTESTATION_KMS_KEY_VERSION=${ATTESTATION_KEY_VERSION},ATTESTATION_ISSUER=${ATTESTATION_ISSUER},VERIFICATION_BASE_URL=${VERIFICATION_BASE_URL},EVM_CHAIN_ID=${EVM_CHAIN_ID},EVM_VERIFIER_CONTRACT=${EVM_VERIFIER_CONTRACT},EVM_PUBLISHER_ADDRESS=${EVM_PUBLISHER_ADDRESS},EVM_PUBLISHER_KMS_KEY_VERSION=${EVM_KEY_VERSION}" \
      --cpu="${CPU:-1}" --memory="${MEMORY:-512Mi}" \
      --min-instances="${MIN_INSTANCES:-0}" --max-instances="${MAX_INSTANCES:-10}" \
      --concurrency="${CONCURRENCY:-20}" --timeout="${TIMEOUT:-60s}" \
      --port=8080 --execution-environment=gen2 \
      --labels="app=${SERVICE_NAME},managed-by=project-validation-infra"
    ;;
  smoke)
    require_command curl
    SERVICE_URL="$(gcloud run services describe "${SERVICE_NAME}" \
      --project="${PROJECT_ID}" --region="${REGION}" --format='value(status.url)')"
    TOKEN="$(gcloud auth print-identity-token)"
    curl -fsS -H "Authorization: Bearer ${TOKEN}" "${SERVICE_URL}/healthz"
    public_invoker="$(gcloud run services get-iam-policy "${SERVICE_NAME}" \
      --project="${PROJECT_ID}" --region="${REGION}" \
      --flatten='bindings[].members' \
      --filter='bindings.role=roles/run.invoker AND bindings.members=allUsers' \
      --format='value(bindings.members)')"
    [[ -z "${public_invoker}" ]] || {
      echo "service is unhealthy: allUsers has roles/run.invoker" >&2
      exit 1
    }
    gcloud run services describe "${SERVICE_NAME}" \
      --project="${PROJECT_ID}" --region="${REGION}" \
      --format='table(status.latestReadyRevisionName,status.traffic.revisionName,status.traffic.percent,spec.template.spec.containers[0].image)'
    ;;
  revisions)
    gcloud run revisions list --service="${SERVICE_NAME}" \
      --project="${PROJECT_ID}" --region="${REGION}" \
      --format='table(metadata.name,status.conditions[0].status,metadata.creationTimestamp,spec.containers[0].image)'
    ;;
  rollback)
    TARGET_REVISION="${2:-}"
    require_var TARGET_REVISION
    gcloud run revisions describe "${TARGET_REVISION}" \
      --project="${PROJECT_ID}" --region="${REGION}" >/dev/null
    gcloud run services update-traffic "${SERVICE_NAME}" \
      --project="${PROJECT_ID}" --region="${REGION}" \
      --to-revisions="${TARGET_REVISION}=100"
    ;;
  *) usage ;;
esac
