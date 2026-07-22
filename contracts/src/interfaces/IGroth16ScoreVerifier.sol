// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @notice Interface implemented by the Solidity verifier exported from the
/// exact BN254 Groth16 verifying key used by internal/zkproof.
/// @dev gnark v0.14 exports verifyProof with fixed-size arrays and signals
/// success by returning normally; invalid proofs revert. This is deliberately
/// not a hash-comparison interface and must not be replaced with one.
interface IGroth16ScoreVerifier {
    function verifyProof(
        uint256[8] calldata proof,
        uint256[2] calldata input
    ) external view;
}
