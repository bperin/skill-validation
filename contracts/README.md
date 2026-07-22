# BrianSkillCo On Chain Proof of Concept

## Runnable Local `rag-ai` MVP

[`SkillMVPRegistry.sol`](src/SkillMVPRegistry.sol) records that a validation-authorized child address co-signed one learner commitment for one skill such as `rag-ai`, one policy version, and one evidence hash. The child transaction is the cryptographic validation endorsement. A developer wallet is bound separately and can sign later project evidence; it has no registration authority.

Run the whole flow from the repository root:

```sh
npm ci
cd ..
cp .env.example .env
go run ./cmd/issuer-demo network
```

In a second terminal, run `go run ./cmd/issuer-demo issue` with a learner, evidence, and holder address as shown in the root [README](../README.md#run-the-rag-ai-mvp-locally). Go submits the root and child registration transactions, but never exports the child validation key. `sign-project` uses the developer holder key and `verify-project` calls the public Solidity verifier. It spends no ETH and makes no public network request.

The central validation authority evaluates the project and decides whether it meets the capability threshold. The contract does not evaluate source code or training output. It preserves a public, portable answer to four questions: was the capability endorsed by an authorized authority, is it current, which evidence hash was validated, and which holder wallet may prove control?

[`SkillRegistry.sol`](src/SkillRegistry.sol) is an EVM compatible registry intended for Arbitrum One (chain ID `42161`). It is a local POC: `npm run deploy:development` deploys it only to Hardhat's ephemeral in-process chain. No public contract address, authenticated Arbitrum RPC request, wallet, funding, transaction, or external chain mutation has been performed.

## Current Skill and Historical Lineage

The root authority can call `supersedeWithProof` to replace the active record for the same subject and skill IDs with newer proof, evidence binding, and program/policy identifier. The old attestation remains stored but inactive. `predecessorOf`, `successorOf`, and `isCurrent` let consumers distinguish the current skill validation from its historical lineage. This is the local POC for a moving skill definition; it is not an external deployment or end-to-end integration.

`delegateIssuerFor` adds the narrower registration form: the immutable root delegates one child address for one subject commitment, skill ID, program or policy ID, scope, and expiry. `attestWithProof` rejects that child if any of those fields differ or the delegation has expired. The child address is validation-authorized, not a learner wallet.

## Developer Holder Binding

After an active validation proof exists, its original authority or the root may call `bindHolder(attestationId, holder, expiresAt)`. This stores one developer owned EVM address for presentation and project evidence proof. `isHolderAuthorized` lets an external verifier check the binding and expiration; `revokeHolderBinding` stops the binding without revoking the proof.

This does not grant registration authority. The holder address cannot call `attest`, update a proof, or revoke a proof. `holderProjectDigest` domain binds a project evidence hash to this registry and chain; `verifyHolderProject` verifies the holder's EIP-191 signature against the active binding. A production verifier should additionally use a nonce based challenge for interactive proof of control.

The immutable root can be a `BrianSkillCo` controlled protected signer or the executing address of a community governance system such as a multisig or timelock. This registry records the authorized policy transition; it does not implement voting or consensus itself.

## What Becomes Public

The registry stores a subject commitment, proof commitment, evidence binding, program and skill identifiers, delegated validation address, scope, timestamps, and revocation state. A verifier can check `hasSkill(subjectCommitment, skillId)` without seeing a learner ID, project, score, salt, or execution logs.

`evidenceBinding` is `keccak256` over a canonical, domain separated authority payload containing the GitHub repository URL, commit SHA, workflow-run ID, and artifact or deployment digest. The learner receives a proof reference, not a child-key private key. A `/.well-known/project-attestation.json` or `/api/attestation` consumer must compute the attestation ID and evidence binding, then call `verifyAttestation(attestationId, evidenceBinding)` and `isIssuerAuthorized(issuer, scope)`. A copied JSON file cannot establish valid validation authority after a child key or proof is revoked.

The proof commitment is the public MiMC-BN254 commitment from `internal/zkproof`: `MiMC(score, salt)`. The contract requires it to match public signal 0. Public signal 1 is the threshold. The Groth16 proof proves that the hidden score is at least that public threshold.

## Validation Authority Hierarchy

The deployment key becomes the immutable `rootIssuer`. It delegates child keys with a bitmask: bit 0 permits `GitHubDeployment` attestations and bit 1 permits `WebPage` attestations. Child keys can be revoked individually. The root remains the on-chain authority and can revoke any attestation. Do not run high-volume registration through the root key.

## Verifier is a Real Cryptographic Boundary

The registry expects the exact fixed-array ABI emitted by gnark BN254 Groth16 `ExportSolidityVerifier`:

```solidity
function verifyProof(
    uint256[8] calldata proof,
    uint256[2] calldata publicInputs
) external view returns (bool)
```

The registry contract validates the proof on-chain to confirm that the learner scored at least the threshold. If verification fails, it reverts.

Supporting unit tests are located in [`contracts/test`](contracts/test).
