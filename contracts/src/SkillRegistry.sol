// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {IGroth16ScoreVerifier} from "./interfaces/IGroth16ScoreVerifier.sol";

/// @title SkillRegistry
/// @notice Stores only proof-backed commitments and skill attestations. It
/// never receives a learner ID, project, raw score, salt, or execution logs.
/// @dev The immutable root issuer controls individually revocable, scoped child
/// issuer keys. Only a verifier generated from the matching Groth16 key is a
/// cryptographic verifier; a mock verifier must never be used in production.
/// A learner/candidate is represented by a privacy-preserving subject
/// commitment, not by an issuer key. They receive a credential; only the
/// issuer's delegated service address may record a new skill.
contract SkillRegistry {
    enum AttestationScope {
        GitHubDeployment,
        WebPage
    }

    struct DelegatedIssuer {
        uint8 scopeMask;
        bool active;
        bytes32 subjectCommitment;
        bytes32 skillId;
        bytes32 programId;
        uint64 expiresAt;
    }

    struct Attestation {
        bytes32 subjectCommitment;
        bytes32 credentialCommitment;
        bytes32 evidenceBinding;
        bytes32 programId;
        address issuer;
        AttestationScope scope;
        uint64 issuedAt;
        uint64 revokedAt;
        bool active;
        bytes32[] skillIds;
    }

    /// @notice A developer-owned public key authorized to prove control of one
    /// active credential. This address cannot issue, update, or revoke skills.
    /// The holder signs presentation challenges off chain; the registry only
    /// makes the issuer-approved binding and its status queryable.
    struct HolderBinding {
        address holder;
        uint64 expiresAt;
        bool active;
    }

    error OnlyRootIssuer();
    error UnauthorizedIssuer(address issuer, AttestationScope scope);
    error InvalidAddress();
    error EmptySkills();
    error InvalidCommitment();
    error DuplicateAttestation(bytes32 attestationId);
    error UnknownAttestation(bytes32 attestationId);
    error AlreadyRevoked(bytes32 attestationId);
    error PublicCommitmentMismatch();
    error SkillAlreadyActive(bytes32 subjectCommitment, bytes32 skillId);
    error RootUpdateRequiresPredecessor();
    error DelegationExpired(address issuer);
    error DelegationSubjectMismatch(address issuer);
    error DelegationSkillMismatch(address issuer);
    error DelegationProgramMismatch(address issuer);
    error InvalidDelegation();
    error UnauthorizedHolderBindingIssuer(address issuer);
    error HolderBindingActive(bytes32 attestationId);
    error InvalidHolderBinding();

    event IssuerDelegated(address indexed child, uint8 scopeMask);
    event IssuerDelegatedFor(
        address indexed child,
        uint8 scopeMask,
        bytes32 indexed subjectCommitment,
        bytes32 skillId,
        bytes32 programId,
        uint64 expiresAt
    );
    event IssuerRevoked(address indexed child);
    event AttestationRecorded(
        bytes32 indexed attestationId,
        bytes32 indexed subjectCommitment,
        bytes32 indexed credentialCommitment,
        bytes32 evidenceBinding,
        bytes32 programId,
        address issuer,
        AttestationScope scope
    );
    event AttestationRevoked(bytes32 indexed attestationId, address indexed revokedBy);
    event AttestationSuperseded(bytes32 indexed predecessor, bytes32 indexed successor, bytes32 indexed subjectCommitment);
    event HolderBound(bytes32 indexed attestationId, address indexed holder, uint64 expiresAt, address indexed issuer);
    event HolderBindingRevoked(bytes32 indexed attestationId, address indexed revokedBy);

    address public immutable rootIssuer;
    IGroth16ScoreVerifier public immutable verifier;

    mapping(address => DelegatedIssuer) public delegatedIssuers;
    mapping(bytes32 => Attestation) private attestations;
    mapping(bytes32 => HolderBinding) private holderBindings;
    mapping(bytes32 => mapping(bytes32 => bytes32)) private activeSkillAttestation;
    mapping(bytes32 => bytes32) public successorOf;
    mapping(bytes32 => bytes32) public predecessorOf;

    constructor(IGroth16ScoreVerifier verifier_) {
        if (address(verifier_) == address(0)) revert InvalidAddress();
        rootIssuer = msg.sender;
        verifier = verifier_;
    }

    modifier onlyRootIssuer() {
        if (msg.sender != rootIssuer) revert OnlyRootIssuer();
        _;
    }

    /// @notice Delegate one or both supported issuance scopes to an
    /// issuer-controlled child address.
    /// @dev scopeMask bit 0 is GitHubDeployment and bit 1 is WebPage.
    /// Never delegate this address to a learner just because they are the
    /// credential subject. Doing so would let them mint their own skills.
    function delegateIssuer(address child, uint8 scopeMask) external onlyRootIssuer {
        if (child == address(0) || scopeMask == 0 || scopeMask > 3) revert InvalidAddress();
        delegatedIssuers[child] = DelegatedIssuer({
            scopeMask: scopeMask,
            active: true,
            subjectCommitment: bytes32(0),
            skillId: bytes32(0),
            programId: bytes32(0),
            expiresAt: 0
        });
        emit IssuerDelegated(child, scopeMask);
    }

    /// @notice Delegate a child address for exactly one subject, skill, policy,
    /// and expiry. The child remains issuer controlled; this is not a learner
    /// wallet or permission to mint arbitrary skills.
    function delegateIssuerFor(
        address child,
        uint8 scopeMask,
        bytes32 subjectCommitment,
        bytes32 skillId,
        bytes32 programId,
        uint64 expiresAt
    ) external onlyRootIssuer {
        if (
            child == address(0) || scopeMask == 0 || scopeMask > 3 || subjectCommitment == bytes32(0) ||
            skillId == bytes32(0) || programId == bytes32(0) || expiresAt <= block.timestamp
        ) revert InvalidDelegation();
        delegatedIssuers[child] = DelegatedIssuer({
            scopeMask: scopeMask,
            active: true,
            subjectCommitment: subjectCommitment,
            skillId: skillId,
            programId: programId,
            expiresAt: expiresAt
        });
        emit IssuerDelegatedFor(child, scopeMask, subjectCommitment, skillId, programId, expiresAt);
    }

    /// @notice Disable a child issuer immediately without changing other keys.
    function revokeIssuer(address child) external onlyRootIssuer {
        DelegatedIssuer storage issuer = delegatedIssuers[child];
        issuer.active = false;
        issuer.scopeMask = 0;
        emit IssuerRevoked(child);
    }

    /// @notice Record a Groth16-verified, privacy-preserving skill attestation.
    /// @param input [0] is the MiMC(score, salt) commitment and [1] is the
    /// public threshold. The verifier reverts if the hidden score is lower.
    /// @dev msg.sender must be an active issuer-owned child address with the
    /// required scope. A random wallet or learner wallet is rejected before any
    /// attestation is written.
    function attestWithProof(
        bytes32 subjectCommitment,
        bytes32 credentialCommitment,
        bytes32 evidenceBinding,
        bytes32 programId,
        AttestationScope scope,
        bytes32[] calldata skillIds,
        uint256[8] calldata proof,
        uint256[2] calldata input
    ) external {
        if (msg.sender == rootIssuer) revert RootUpdateRequiresPredecessor();
        _requireDelegatedAttestation(msg.sender, scope, subjectCommitment, programId, skillIds);
        if (subjectCommitment == bytes32(0) || credentialCommitment == bytes32(0) || programId == bytes32(0) || evidenceBinding == bytes32(0)) {
            revert InvalidCommitment();
        }
        if (skillIds.length == 0) revert EmptySkills();
        _verifyCredentialProof(credentialCommitment, proof, input);

        bytes32 attestationId = _attestationId(subjectCommitment, credentialCommitment);
        if (attestations[attestationId].issuedAt != 0) revert DuplicateAttestation(attestationId);

        Attestation storage attestation = attestations[attestationId];
        attestation.subjectCommitment = subjectCommitment;
        attestation.credentialCommitment = credentialCommitment;
        attestation.evidenceBinding = evidenceBinding;
        attestation.programId = programId;
        attestation.issuer = msg.sender;
        attestation.scope = scope;
        attestation.issuedAt = uint64(block.timestamp);
        attestation.active = true;

        for (uint256 i; i < skillIds.length; ++i) {
            bytes32 skillId = skillIds[i];
            if (skillId == bytes32(0)) revert InvalidCommitment();
            if (activeSkillAttestation[subjectCommitment][skillId] != bytes32(0)) {
                revert SkillAlreadyActive(subjectCommitment, skillId);
            }
            attestation.skillIds.push(skillId);
            activeSkillAttestation[subjectCommitment][skillId] = attestationId;
        }

        emit AttestationRecorded(attestationId, subjectCommitment, credentialCommitment, evidenceBinding, programId, msg.sender, scope);
    }

    /// @notice Replace an active attestation with a newer root-authorized
    /// record for the same subject and skills. The prior record remains
    /// queryable but is no longer current; the two records are linked.
    /// @dev A changed program ID represents a revised evaluation policy or
    /// skill definition. The root must be a protected signing authority.
    function supersedeWithProof(
        bytes32 predecessor,
        bytes32 credentialCommitment,
        bytes32 evidenceBinding,
        bytes32 programId,
        uint256[8] calldata proof,
        uint256[2] calldata input
    ) external onlyRootIssuer {
        Attestation storage previous = attestations[predecessor];
        if (previous.issuedAt == 0) revert UnknownAttestation(predecessor);
        if (!previous.active) revert AlreadyRevoked(predecessor);
        if (credentialCommitment == bytes32(0) || evidenceBinding == bytes32(0) || programId == bytes32(0)) {
            revert InvalidCommitment();
        }
        if (bytes32(input[0]) != credentialCommitment) revert PublicCommitmentMismatch();

        verifier.verifyProof(proof, input);

        bytes32 successor = _attestationId(previous.subjectCommitment, credentialCommitment);
        if (attestations[successor].issuedAt != 0) revert DuplicateAttestation(successor);

        Attestation storage updated = attestations[successor];
        updated.subjectCommitment = previous.subjectCommitment;
        updated.credentialCommitment = credentialCommitment;
        updated.evidenceBinding = evidenceBinding;
        updated.programId = programId;
        updated.issuer = rootIssuer;
        updated.scope = previous.scope;
        updated.issuedAt = uint64(block.timestamp);
        updated.active = true;

        for (uint256 i; i < previous.skillIds.length; ++i) {
            bytes32 skillId = previous.skillIds[i];
            updated.skillIds.push(skillId);
            activeSkillAttestation[previous.subjectCommitment][skillId] = successor;
        }

        previous.active = false;
        previous.revokedAt = uint64(block.timestamp);
        successorOf[predecessor] = successor;
        predecessorOf[successor] = predecessor;
        emit AttestationRecorded(successor, updated.subjectCommitment, credentialCommitment, evidenceBinding, programId, rootIssuer, updated.scope);
        emit AttestationSuperseded(predecessor, successor, updated.subjectCommitment);
    }

    /// @notice Revoke a credential and remove each of its live skill checks.
    /// Either the immutable root or the original delegated issuer may revoke.
    function revokeAttestation(bytes32 attestationId) external {
        Attestation storage attestation = attestations[attestationId];
        if (attestation.issuedAt == 0) revert UnknownAttestation(attestationId);
        if (!attestation.active) revert AlreadyRevoked(attestationId);
        if (msg.sender != rootIssuer && msg.sender != attestation.issuer) revert OnlyRootIssuer();

        attestation.active = false;
        attestation.revokedAt = uint64(block.timestamp);
        for (uint256 i; i < attestation.skillIds.length; ++i) {
            bytes32 skillId = attestation.skillIds[i];
            if (activeSkillAttestation[attestation.subjectCommitment][skillId] == attestationId) {
                delete activeSkillAttestation[attestation.subjectCommitment][skillId];
            }
        }
        emit AttestationRevoked(attestationId, msg.sender);
    }

    /// @notice Bind a developer-controlled address to one credential for
    /// presentation and project-evidence proofs. The holder address is not an
    /// issuer child and receives no skill-minting permission.
    function bindHolder(bytes32 attestationId, address holder, uint64 expiresAt) external {
        Attestation storage attestation = attestations[attestationId];
        if (attestation.issuedAt == 0) revert UnknownAttestation(attestationId);
        if (!attestation.active || holder == address(0) || expiresAt <= block.timestamp) revert InvalidHolderBinding();
        _requireHolderBindingIssuer(attestation);

        HolderBinding storage binding = holderBindings[attestationId];
        if (binding.active && binding.expiresAt > block.timestamp) revert HolderBindingActive(attestationId);
        binding.holder = holder;
        binding.expiresAt = expiresAt;
        binding.active = true;
        emit HolderBound(attestationId, holder, expiresAt, msg.sender);
    }

    /// @notice Stop accepting holder-control proofs for this credential without
    /// affecting the credential itself or any issuer delegation.
    function revokeHolderBinding(bytes32 attestationId) external {
        Attestation storage attestation = attestations[attestationId];
        if (attestation.issuedAt == 0) revert UnknownAttestation(attestationId);
        _requireHolderBindingIssuer(attestation);
        HolderBinding storage binding = holderBindings[attestationId];
        if (!binding.active) revert InvalidHolderBinding();
        binding.active = false;
        emit HolderBindingRevoked(attestationId, msg.sender);
    }

    function hasSkill(bytes32 subjectCommitment, bytes32 skillId) external view returns (bool) {
        bytes32 attestationId = activeSkillAttestation[subjectCommitment][skillId];
        return attestationId != bytes32(0) && attestations[attestationId].active;
    }

    /// @notice Returns true only for the active tip of an attestation lineage.
    function isCurrent(bytes32 attestationId) external view returns (bool) {
        return attestations[attestationId].active && successorOf[attestationId] == bytes32(0);
    }

    /// @notice Public verification surface for JSON, badge, and API consumers.
    /// A copied credential fails this check unless it retains its original
    /// on-chain id, evidence binding, active state, and delegated issuer.
    function verifyAttestation(bytes32 attestationId, bytes32 evidenceBinding) external view returns (bool) {
        Attestation storage attestation = attestations[attestationId];
        return attestation.active && attestation.evidenceBinding == evidenceBinding && _isScopedIssuer(attestation.issuer, attestation.scope);
    }

    function isIssuerAuthorized(address issuer, AttestationScope scope) external view returns (bool) {
        return _isScopedIssuer(issuer, scope);
    }

    /// @notice Returns whether an active credential currently recognizes this
    /// developer-owned holder address. Callers must separately verify the
    /// holder's fresh off-chain challenge signature.
    function isHolderAuthorized(bytes32 attestationId, address holder) external view returns (bool) {
        Attestation storage attestation = attestations[attestationId];
        HolderBinding storage binding = holderBindings[attestationId];
        return attestation.active && binding.active && binding.holder == holder && binding.expiresAt > block.timestamp;
    }

    function getHolderBinding(bytes32 attestationId) external view returns (address holder, uint64 expiresAt, bool active) {
        HolderBinding storage binding = holderBindings[attestationId];
        return (binding.holder, binding.expiresAt, binding.active);
    }

    function getAttestation(bytes32 attestationId) external view returns (
        bytes32 subjectCommitment,
        bytes32 credentialCommitment,
        bytes32 evidenceBinding,
        bytes32 programId,
        address issuer,
        AttestationScope scope,
        uint64 issuedAt,
        uint64 revokedAt,
        bool active,
        bytes32[] memory skillIds
    ) {
        Attestation storage attestation = attestations[attestationId];
        return (
            attestation.subjectCommitment,
            attestation.credentialCommitment,
            attestation.evidenceBinding,
            attestation.programId,
            attestation.issuer,
            attestation.scope,
            attestation.issuedAt,
            attestation.revokedAt,
            attestation.active,
            attestation.skillIds
        );
    }

    function deriveAttestationId(bytes32 subjectCommitment, bytes32 credentialCommitment) external pure returns (bytes32) {
        return _attestationId(subjectCommitment, credentialCommitment);
    }

    function _requireScopedIssuer(address issuer, AttestationScope scope) private view returns (DelegatedIssuer memory delegation) {
        if (!_isScopedIssuer(issuer, scope)) {
            revert UnauthorizedIssuer(issuer, scope);
        }
        return delegatedIssuers[issuer];
    }

    function _requireHolderBindingIssuer(Attestation storage attestation) private view {
        if (msg.sender == rootIssuer) return;
        if (msg.sender != attestation.issuer || !_isScopedIssuer(msg.sender, attestation.scope)) {
            revert UnauthorizedHolderBindingIssuer(msg.sender);
        }
    }

    function _requireDelegatedAttestation(
        address issuer,
        AttestationScope scope,
        bytes32 subjectCommitment,
        bytes32 programId,
        bytes32[] calldata skillIds
    ) private view {
        DelegatedIssuer memory delegation = _requireScopedIssuer(issuer, scope);
        _requireDelegationMatch(delegation, issuer, subjectCommitment, programId, skillIds);
    }

    function _verifyCredentialProof(
        bytes32 credentialCommitment,
        uint256[8] calldata proof,
        uint256[2] calldata input
    ) private view {
        if (bytes32(input[0]) != credentialCommitment) revert PublicCommitmentMismatch();
        // The call either verifies the real proof or reverts. It must point to
        // a verifier generated from the same proving/verifying-key ceremony.
        verifier.verifyProof(proof, input);
    }

    function _requireDelegationMatch(
        DelegatedIssuer memory delegation,
        address issuer,
        bytes32 subjectCommitment,
        bytes32 programId,
        bytes32[] calldata skillIds
    ) private view {
        // A zero subject commitment marks the older broad scope-only
        // delegation path. delegateIssuerFor supplies all four restrictions.
        if (delegation.subjectCommitment == bytes32(0)) return;
        if (block.timestamp >= delegation.expiresAt) revert DelegationExpired(issuer);
        if (delegation.subjectCommitment != subjectCommitment) revert DelegationSubjectMismatch(issuer);
        if (delegation.programId != programId) revert DelegationProgramMismatch(issuer);
        if (skillIds.length != 1 || skillIds[0] != delegation.skillId) revert DelegationSkillMismatch(issuer);
    }

    function _isScopedIssuer(address issuer, AttestationScope scope) private view returns (bool) {
        if (issuer == rootIssuer) return true;
        DelegatedIssuer memory delegated = delegatedIssuers[issuer];
        uint8 requiredScope = uint8(1 << uint8(scope));
        return delegated.active && (delegated.scopeMask & requiredScope) != 0;
    }

    function _attestationId(bytes32 subjectCommitment, bytes32 credentialCommitment) private pure returns (bytes32) {
        return keccak256(abi.encode(subjectCommitment, credentialCommitment));
    }
}
