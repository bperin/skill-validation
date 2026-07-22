# Cloud Run Deployment

This directory contains the deployment authority and configuration for the private `BrianSkillCo` attestation issuer. The runtime uses one digest-pinned Artifact Registry image, a dedicated service account, private-IP Cloud SQL PostgreSQL, Secret Manager, and two non-exportable Cloud KMS signing keys.

The P-256 key signs off-chain attestation payloads. The separate `secp256k1` key signs transactions that publish only a ZK proof/public commitment to the configured EVM verifier. Credentials, learner PII, scores, logs, and complete attestations remain off-chain. These scripts do not deploy a verifier contract or fund/send blockchain transactions.

## Configure

```sh
cp infra/config.env.example infra/config.env
```

Fill every blank. `PROJECT_ID`, chain ID, verifier contract, issuer URLs, and publisher address are intentionally not inferred. Put the database password, issuer token, and authenticated EVM RPC URL in permission-restricted files outside the repository and reference those paths only during provisioning.

The default Cloud SQL shape is zonal. Set `CLOUD_SQL_AVAILABILITY_TYPE=REGIONAL` for production after accepting the additional cost. The instance is deletion-protected, private-IP-only, and connected to Cloud Run through Direct VPC egress plus the Cloud SQL connector.

## Provision

```sh
CONFIG_FILE=infra/config.env ./infra/provision.sh
```

Provisioning enables required APIs and idempotently creates the Docker repository, runtime identity, VPC/subnet/private-service peering, Cloud SQL instance/database/user, secrets, KMS keys, and least-privilege bindings. Runtime project roles are limited to Cloud SQL client, log writer, and metric writer. Secret access is granted per secret and signing access per KMS key.

Re-running provisioning does not rotate an existing database user unless `ROTATE_DATABASE_CREDENTIALS=true`. Secret versions are added only when their source file is configured.

## Build and Deploy

```sh
./infra/validate.sh
CONFIG_FILE=infra/config.env ./infra/build.sh
CONFIG_FILE=infra/config.env ./infra/gcloud.sh deploy
CONFIG_FILE=infra/config.env ./infra/gcloud.sh smoke
```

`build.sh` refuses a dirty worktree by default, tags the image with the full Git commit, resolves its SHA-256 digest, and stores only the non-secret digest reference in ignored `infra/.last-image`. Deployment rejects mutable tags and keeps the service authenticated; no `allUsers` invoker grant is made.

Grant `roles/run.invoker` on the service only to named issuer callers. The public recruiter proof page should be a separate read-only surface and must never expose this private issuer endpoint.

## Rollback

```sh
CONFIG_FILE=infra/config.env ./infra/gcloud.sh revisions
CONFIG_FILE=infra/config.env ./infra/gcloud.sh rollback project-validation-00001-abc
CONFIG_FILE=infra/config.env ./infra/gcloud.sh smoke
```

Rollback changes traffic to an existing immutable revision; it does not rebuild, alter the ledger, rotate keys, or reverse an already-published on-chain proof.

## EVM Signer Boundary

Cloud KMS supports `EC_SIGN_SECP256K1_SHA256`, so the publisher key is non-exportable. Transaction code must correctly convert KMS DER signatures to Ethereum `(r,s,v)`, enforce low-S normalization, derive/check the configured publisher address from the KMS public key, use chain-aware transaction signing, and make nonce allocation durable. Treat a mismatch between the derived and configured publisher address as a startup failure.

Contract deployment, network selection, wallet funding, and gas spending require an explicit release decision outside these scripts.
