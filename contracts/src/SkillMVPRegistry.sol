// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @title SkillMVPRegistry
/// @notice A small, deployable registry for issuer-certified skill claims.
/// @dev This contract proves issuer authorization, record integrity, holder
/// binding, revocation, and policy lineage. It stores commitments only: raw
/// learner identifiers and project evidence stay off chain. It intentionally
/// does not claim to verify a private score or a ZK proof; use SkillRegistry
/// with a generated Groth16 verifier for that higher-assurance path.
contract SkillMVPRegistry {
    struct DelegatedIssuer {
        bytes32 subjectCommitment;
        bytes32 skillId;
        bytes32 programId;
        uint64 expiresAt;
        bool active;
    }

    struct Attestation {
        bytes32 subjectCommitment;
        bytes32 skillId;
        bytes32 programId;
        bytes32 evidenceBinding;
        address issuer;
        uint64 issuedAt;
        uint64 revokedAt;
        bool active;
    }

    struct HolderBinding {
        address holder;
        uint64 expiresAt;
        bool active;
    }

    error OnlyRootIssuer();
    error UnauthorizedIssuer(address issuer);
    error InvalidAddress();
    error InvalidCommitment();
    error InvalidExpiry();
    error UnknownAttestation(bytes32 attestationId);
    error DuplicateAttestation(bytes32 attestationId);
    error SkillAlreadyActive(bytes32 subjectCommitment, bytes32 skillId);
    error AlreadyRevoked(bytes32 attestationId);
    error HolderBindingActive(bytes32 attestationId);

    event IssuerDelegated(
        address indexed child,
        bytes32 indexed subjectCommitment,
        bytes32 indexed skillId,
        bytes32 programId,
        uint64 expiresAt
    );
    event IssuerRevoked(address indexed child);
    event AttestationRecorded(
        bytes32 indexed attestationId,
        bytes32 indexed subjectCommitment,
        bytes32 indexed skillId,
        bytes32 programId,
        bytes32 evidenceBinding,
        address issuer
    );
    event AttestationRevoked(bytes32 indexed attestationId, address indexed revokedBy);
    event AttestationSuperseded(bytes32 indexed predecessor, bytes32 indexed successor);
    event HolderBound(bytes32 indexed attestationId, address indexed holder, uint64 expiresAt);
    event HolderBindingRevoked(bytes32 indexed attestationId);

    address public immutable rootIssuer;
    mapping(address => DelegatedIssuer) public delegatedIssuers;
    mapping(bytes32 => Attestation) private attestations;
    mapping(bytes32 => HolderBinding) private holderBindings;
    mapping(bytes32 => mapping(bytes32 => bytes32)) private activeSkillAttestation;
    mapping(bytes32 => bytes32) public predecessorOf;
    mapping(bytes32 => bytes32) public successorOf;

    constructor() {
        rootIssuer = msg.sender;
    }

    modifier onlyRootIssuer() {
        if (msg.sender != rootIssuer) revert OnlyRootIssuer();
        _;
    }

    /// @notice Give an issuer-controlled child address permission to certify
    /// exactly one learner, skill, and evaluation policy until expiry.
    function delegateIssuerFor(
        address child,
        bytes32 subjectCommitment,
        bytes32 skillId,
        bytes32 programId,
        uint64 expiresAt
    ) external onlyRootIssuer {
        if (child == address(0)) revert InvalidAddress();
        if (subjectCommitment == bytes32(0) || skillId == bytes32(0) || programId == bytes32(0)) revert InvalidCommitment();
        if (expiresAt <= block.timestamp) revert InvalidExpiry();
        delegatedIssuers[child] = DelegatedIssuer(subjectCommitment, skillId, programId, expiresAt, true);
        emit IssuerDelegated(child, subjectCommitment, skillId, programId, expiresAt);
    }

    function revokeIssuer(address child) external onlyRootIssuer {
        delegatedIssuers[child].active = false;
        emit IssuerRevoked(child);
    }

    /// @notice Commit a completed skill evaluation. evidenceBinding is an
    /// arbitrary domain-separated hash, for example a repository commit and
    /// artifact digest. The issuing EOA transaction is the child key signature.
    function attest(
        bytes32 subjectCommitment,
        bytes32 skillId,
        bytes32 programId,
        bytes32 evidenceBinding
    ) external {
        if (subjectCommitment == bytes32(0) || skillId == bytes32(0) || programId == bytes32(0) || evidenceBinding == bytes32(0)) {
            revert InvalidCommitment();
        }
        _requireDelegatedIssuer(msg.sender, subjectCommitment, skillId, programId);
        if (activeSkillAttestation[subjectCommitment][skillId] != bytes32(0)) {
            revert SkillAlreadyActive(subjectCommitment, skillId);
        }

        bytes32 attestationId = _attestationId(subjectCommitment, skillId, programId, evidenceBinding);
        if (attestations[attestationId].issuedAt != 0) revert DuplicateAttestation(attestationId);
        attestations[attestationId] = Attestation({
            subjectCommitment: subjectCommitment,
            skillId: skillId,
            programId: programId,
            evidenceBinding: evidenceBinding,
            issuer: msg.sender,
            issuedAt: uint64(block.timestamp),
            revokedAt: 0,
            active: true
        });
        activeSkillAttestation[subjectCommitment][skillId] = attestationId;
        emit AttestationRecorded(attestationId, subjectCommitment, skillId, programId, evidenceBinding, msg.sender);
    }

    /// @notice Replace a current skill after an issuer changes the evaluation
    /// policy or accepts new evidence. The root alone controls this transition.
    function supersede(bytes32 predecessor, bytes32 newProgramId, bytes32 newEvidenceBinding) external onlyRootIssuer {
        Attestation storage previous = attestations[predecessor];
        if (previous.issuedAt == 0) revert UnknownAttestation(predecessor);
        if (!previous.active) revert AlreadyRevoked(predecessor);
        if (newProgramId == bytes32(0) || newEvidenceBinding == bytes32(0)) revert InvalidCommitment();

        bytes32 successor = _attestationId(previous.subjectCommitment, previous.skillId, newProgramId, newEvidenceBinding);
        if (attestations[successor].issuedAt != 0) revert DuplicateAttestation(successor);
        attestations[successor] = Attestation({
            subjectCommitment: previous.subjectCommitment,
            skillId: previous.skillId,
            programId: newProgramId,
            evidenceBinding: newEvidenceBinding,
            issuer: rootIssuer,
            issuedAt: uint64(block.timestamp),
            revokedAt: 0,
            active: true
        });
        previous.active = false;
        previous.revokedAt = uint64(block.timestamp);
        activeSkillAttestation[previous.subjectCommitment][previous.skillId] = successor;
        successorOf[predecessor] = successor;
        predecessorOf[successor] = predecessor;
        emit AttestationRecorded(successor, previous.subjectCommitment, previous.skillId, newProgramId, newEvidenceBinding, rootIssuer);
        emit AttestationSuperseded(predecessor, successor);
    }

    function revokeAttestation(bytes32 attestationId) external {
        Attestation storage attestation = attestations[attestationId];
        if (attestation.issuedAt == 0) revert UnknownAttestation(attestationId);
        if (!attestation.active) revert AlreadyRevoked(attestationId);
        if (msg.sender != rootIssuer && msg.sender != attestation.issuer) revert UnauthorizedIssuer(msg.sender);
        attestation.active = false;
        attestation.revokedAt = uint64(block.timestamp);
        if (activeSkillAttestation[attestation.subjectCommitment][attestation.skillId] == attestationId) {
            delete activeSkillAttestation[attestation.subjectCommitment][attestation.skillId];
        }
        emit AttestationRevoked(attestationId, msg.sender);
    }

    /// @notice Bind a developer wallet to one active credential. The wallet can
    /// prove control off chain but cannot certify or modify any skill.
    function bindHolder(bytes32 attestationId, address holder, uint64 expiresAt) external {
        Attestation storage attestation = attestations[attestationId];
        if (attestation.issuedAt == 0) revert UnknownAttestation(attestationId);
        if (!attestation.active || holder == address(0)) revert InvalidAddress();
        if (expiresAt <= block.timestamp) revert InvalidExpiry();
        if (msg.sender != rootIssuer && msg.sender != attestation.issuer) revert UnauthorizedIssuer(msg.sender);
        HolderBinding storage binding = holderBindings[attestationId];
        if (binding.active && binding.expiresAt > block.timestamp) revert HolderBindingActive(attestationId);
        holderBindings[attestationId] = HolderBinding(holder, expiresAt, true);
        emit HolderBound(attestationId, holder, expiresAt);
    }

    function revokeHolderBinding(bytes32 attestationId) external {
        Attestation storage attestation = attestations[attestationId];
        if (attestation.issuedAt == 0) revert UnknownAttestation(attestationId);
        if (msg.sender != rootIssuer && msg.sender != attestation.issuer) revert UnauthorizedIssuer(msg.sender);
        holderBindings[attestationId].active = false;
        emit HolderBindingRevoked(attestationId);
    }

    function hasSkill(bytes32 subjectCommitment, bytes32 skillId) external view returns (bool) {
        bytes32 attestationId = activeSkillAttestation[subjectCommitment][skillId];
        return attestationId != bytes32(0) && attestations[attestationId].active;
    }

    /// @notice Returns the current attestation ID for a learner and skill, or
    /// bytes32(0) when no active credential exists. This is the easy public
    /// lookup surface for a portfolio, ATS, or any other verifier UI.
    function currentAttestationId(bytes32 subjectCommitment, bytes32 skillId) external view returns (bytes32) {
        return activeSkillAttestation[subjectCommitment][skillId];
    }

    function verifyAttestation(bytes32 attestationId, bytes32 evidenceBinding) external view returns (bool) {
        Attestation storage attestation = attestations[attestationId];
        return attestation.active && attestation.evidenceBinding == evidenceBinding;
    }

    function isCurrent(bytes32 attestationId) external view returns (bool) {
        return attestations[attestationId].active && successorOf[attestationId] == bytes32(0);
    }

    function isHolderAuthorized(bytes32 attestationId, address holder) external view returns (bool) {
        HolderBinding storage binding = holderBindings[attestationId];
        return attestations[attestationId].active && binding.active && binding.holder == holder && binding.expiresAt > block.timestamp;
    }

    /// @notice Verifies that the developer-controlled holder key signed a
    /// project evidence hash while its credential was active. The signed
    /// payload is domain-bound to this contract and chain, so it cannot be
    /// replayed against another registry. This grants no issuer authority.
    function verifyHolderProject(
        bytes32 attestationId,
        bytes32 projectEvidenceBinding,
        bytes calldata signature
    ) external view returns (bool) {
        HolderBinding storage binding = holderBindings[attestationId];
        if (!attestations[attestationId].active || !binding.active || binding.expiresAt <= block.timestamp) return false;
        return _recover(_toEthSignedMessageHash(_holderProjectPayload(attestationId, projectEvidenceBinding)), signature) == binding.holder;
    }

    /// @notice The domain-bound 32 byte payload a developer holder passes to
    /// their wallet's EIP-191 personal-sign method for one project.
    function holderProjectDigest(bytes32 attestationId, bytes32 projectEvidenceBinding) external view returns (bytes32) {
        return _holderProjectPayload(attestationId, projectEvidenceBinding);
    }

    function getAttestation(bytes32 attestationId) external view returns (Attestation memory) {
        return attestations[attestationId];
    }

    function deriveAttestationId(
        bytes32 subjectCommitment,
        bytes32 skillId,
        bytes32 programId,
        bytes32 evidenceBinding
    ) external pure returns (bytes32) {
        return _attestationId(subjectCommitment, skillId, programId, evidenceBinding);
    }

    function _requireDelegatedIssuer(address issuer, bytes32 subjectCommitment, bytes32 skillId, bytes32 programId) private view {
        DelegatedIssuer memory delegation = delegatedIssuers[issuer];
        if (
            !delegation.active || delegation.expiresAt <= block.timestamp || delegation.subjectCommitment != subjectCommitment ||
            delegation.skillId != skillId || delegation.programId != programId
        ) revert UnauthorizedIssuer(issuer);
    }

    function _attestationId(bytes32 subjectCommitment, bytes32 skillId, bytes32 programId, bytes32 evidenceBinding) private pure returns (bytes32) {
        return keccak256(abi.encode(subjectCommitment, skillId, programId, evidenceBinding));
    }

    function _holderProjectPayload(bytes32 attestationId, bytes32 projectEvidenceBinding) private view returns (bytes32) {
        return keccak256(abi.encode(block.chainid, address(this), attestationId, projectEvidenceBinding));
    }

    function _toEthSignedMessageHash(bytes32 payload) private pure returns (bytes32) {
        return keccak256(abi.encodePacked("\x19Ethereum Signed Message:\n32", payload));
    }

    function _recover(bytes32 digest, bytes calldata signature) private pure returns (address) {
        if (signature.length != 65) return address(0);
        bytes32 r;
        bytes32 s;
        uint8 v;
        assembly {
            r := calldataload(signature.offset)
            s := calldataload(add(signature.offset, 32))
            v := byte(0, calldataload(add(signature.offset, 64)))
        }
        if (v < 27) v += 27;
        // Reject high-S signatures to match Ethereum transaction malleability rules.
        if ((v != 27 && v != 28) || s > 0x7fffffffffffffffffffffffffffffff5d576e7357a4501ddfe92f46681b20a0) return address(0);
        return ecrecover(digest, v, r, s);
    }
}
