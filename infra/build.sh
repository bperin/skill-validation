#!/usr/bin/env bash
set -euo pipefail

INFRA_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=infra/lib.sh
source "${INFRA_DIR}/lib.sh"

load_config
require_command gcloud
for name in PROJECT_ID REGION ARTIFACT_REPOSITORY IMAGE_NAME; do
  require_var "${name}"
done

REPO_ROOT="$(cd "${INFRA_DIR}/.." && pwd)"
if [[ "${ALLOW_DIRTY_BUILD:-false}" != "true" ]] && \
  [[ -n "$(git -C "${REPO_ROOT}" status --porcelain)" ]]; then
  echo "refusing to build an uncommitted tree; commit it or set ALLOW_DIRTY_BUILD=true" >&2
  exit 2
fi

IMAGE_TAG="${IMAGE_TAG:-$(git -C "${REPO_ROOT}" rev-parse HEAD)}"
if [[ ! "${IMAGE_TAG}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "IMAGE_TAG must be a full 40-character Git commit SHA" >&2
  exit 2
fi

gcloud builds submit "${REPO_ROOT}" \
  --project="${PROJECT_ID}" \
  --config="${INFRA_DIR}/cloudbuild.yaml" \
  --substitutions="_REGION=${REGION},_REPOSITORY=${ARTIFACT_REPOSITORY},_IMAGE_NAME=${IMAGE_NAME},_IMAGE_TAG=${IMAGE_TAG}"

TAGGED_IMAGE="${REGION}-docker.pkg.dev/${PROJECT_ID}/${ARTIFACT_REPOSITORY}/${IMAGE_NAME}:${IMAGE_TAG}"
IMAGE_DIGEST="$(gcloud artifacts docker images describe "${TAGGED_IMAGE}" \
  --project="${PROJECT_ID}" --format='value(image_summary.digest)')"
if [[ ! "${IMAGE_DIGEST}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
  echo "could not resolve an immutable digest for ${TAGGED_IMAGE}" >&2
  exit 1
fi

printf '%s@%s\n' "${REGION}-docker.pkg.dev/${PROJECT_ID}/${ARTIFACT_REPOSITORY}/${IMAGE_NAME}" \
  "${IMAGE_DIGEST}" > "${INFRA_DIR}/.last-image"
echo "Built immutable image: $(<"${INFRA_DIR}/.last-image")"
