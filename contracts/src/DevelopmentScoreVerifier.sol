// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {IGroth16ScoreVerifier} from "./interfaces/IGroth16ScoreVerifier.sol";

/// @notice DEVELOPMENT-ONLY verifier for local Hardhat wiring tests.
/// @dev It accepts every input and provides zero cryptographic assurance. Never
/// deploy this contract to Arbitrum or configure it as a production registry
/// verifier. Production must use a verifier exported by internal/zkproof from
/// a documented ceremony's exact BN254 Groth16 verifying key.
contract DevelopmentScoreVerifier is IGroth16ScoreVerifier {
    function verifyProof(uint256[8] calldata, uint256[2] calldata) external pure {}
}
