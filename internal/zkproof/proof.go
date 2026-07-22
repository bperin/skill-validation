// Package zkproof creates BN254 Groth16 proofs for privacy-preserving scores.
//
// The circuit proves knowledge of a private score and salt whose MiMC-BN254
// commitment is public, while also proving that the score is at least a public
// threshold. Learner identity, project data, execution logs, score, and salt
// are never public circuit inputs.
package zkproof

import (
	"fmt"
	"io"
	"math/big"

	"github.com/consensys/gnark-crypto/ecc"
	cryptomimc "github.com/consensys/gnark-crypto/ecc/bn254/fr/mimc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	stdmimc "github.com/consensys/gnark/std/hash/mimc"
)

// Claim contains the private witness and public policy input for a score proof.
type Claim struct {
	Score     *big.Int
	Salt      *big.Int
	Threshold *big.Int
}

// PublicInputs are the only score-proof values suitable for publication.
type PublicInputs struct {
	Commitment *big.Int
	Threshold  *big.Int
}

// System owns a circuit-specific common reference string generated for this
// local proof of concept. A production deployment must use a documented
// multi-party ceremony and persist the matching proving/verifying key assets.
type System struct {
	ccs constraint.ConstraintSystem
	pk  groth16.ProvingKey
	vk  groth16.VerifyingKey
}

type scoreCircuit struct {
	Score      frontend.Variable
	Salt       frontend.Variable
	Commitment frontend.Variable `gnark:",public"`
	Threshold  frontend.Variable `gnark:",public"`
}

func (c *scoreCircuit) Define(api frontend.API) error {
	hash, err := stdmimc.NewMiMC(api)
	if err != nil {
		return fmt.Errorf("create MiMC gadget: %w", err)
	}
	hash.Write(c.Score, c.Salt)
	api.AssertIsEqual(hash.Sum(), c.Commitment)
	api.AssertIsLessOrEqual(c.Threshold, c.Score)
	return nil
}

// NewSystem compiles the BN254 R1CS circuit and performs a local Groth16 setup.
// It is deliberately local-only: it neither calls an RPC endpoint nor persists
// a proving key.
func NewSystem() (*System, error) {
	ccs, err := frontend.Compile(ecc.BN254.ScalarField(), r1cs.NewBuilder, &scoreCircuit{})
	if err != nil {
		return nil, fmt.Errorf("compile score circuit: %w", err)
	}
	pk, vk, err := groth16.Setup(ccs)
	if err != nil {
		return nil, fmt.Errorf("Groth16 setup: %w", err)
	}
	return &System{ccs: ccs, pk: pk, vk: vk}, nil
}

// Prove produces a Groth16 proof and its public inputs. Score and salt only
// appear in the private witness.
func (s *System) Prove(claim Claim) (groth16.Proof, PublicInputs, error) {
	if s == nil || s.ccs == nil || s.pk == nil {
		return nil, PublicInputs{}, fmt.Errorf("nil proof system")
	}
	if err := validateClaim(claim); err != nil {
		return nil, PublicInputs{}, err
	}
	commitment, err := Commitment(claim.Score, claim.Salt)
	if err != nil {
		return nil, PublicInputs{}, err
	}
	assignment := &scoreCircuit{
		Score:      claim.Score,
		Salt:       claim.Salt,
		Commitment: commitment,
		Threshold:  claim.Threshold,
	}
	witness, err := frontend.NewWitness(assignment, ecc.BN254.ScalarField())
	if err != nil {
		return nil, PublicInputs{}, fmt.Errorf("build witness: %w", err)
	}
	proof, err := groth16.Prove(s.ccs, s.pk, witness)
	if err != nil {
		return nil, PublicInputs{}, fmt.Errorf("prove score threshold claim: %w", err)
	}
	return proof, PublicInputs{Commitment: commitment, Threshold: new(big.Int).Set(claim.Threshold)}, nil
}

// Verify checks a proof against only the public commitment and threshold.
func (s *System) Verify(proof groth16.Proof, public PublicInputs) error {
	if s == nil || s.vk == nil {
		return fmt.Errorf("nil proof system")
	}
	if proof == nil || public.Commitment == nil || public.Threshold == nil {
		return fmt.Errorf("proof and public inputs are required")
	}
	assignment := &scoreCircuit{Commitment: public.Commitment, Threshold: public.Threshold}
	witness, err := frontend.NewWitness(assignment, ecc.BN254.ScalarField(), frontend.PublicOnly())
	if err != nil {
		return fmt.Errorf("build public witness: %w", err)
	}
	if err := groth16.Verify(proof, s.vk, witness); err != nil {
		return fmt.Errorf("verify Groth16 proof: %w", err)
	}
	return nil
}

// ExportSolidityVerifier writes the BN254 Groth16 verifier for this system's
// exact verifying key. The generated Solidity must be deployed together with
// proofs made by the matching proving key.
func (s *System) ExportSolidityVerifier(w io.Writer) error {
	if s == nil || s.vk == nil {
		return fmt.Errorf("nil proof system")
	}
	if err := s.vk.ExportSolidity(w); err != nil {
		return fmt.Errorf("export BN254 Solidity verifier: %w", err)
	}
	return nil
}

// Commitment returns the MiMC-BN254 field commitment used inside the circuit.
// It is not SHA-256 and must not be substituted with a hash check masquerading
// as a zero-knowledge proof.
func Commitment(score, salt *big.Int) (*big.Int, error) {
	if score == nil || salt == nil || score.Sign() < 0 || salt.Sign() < 0 {
		return nil, fmt.Errorf("score and salt must be non-negative")
	}
	var scoreBytes, saltBytes [32]byte
	score.FillBytes(scoreBytes[:])
	salt.FillBytes(saltBytes[:])
	hash := cryptomimc.NewMiMC()
	if _, err := hash.Write(scoreBytes[:]); err != nil {
		return nil, fmt.Errorf("hash score: %w", err)
	}
	if _, err := hash.Write(saltBytes[:]); err != nil {
		return nil, fmt.Errorf("hash salt: %w", err)
	}
	return new(big.Int).SetBytes(hash.Sum(nil)), nil
}

func validateClaim(claim Claim) error {
	if claim.Score == nil || claim.Salt == nil || claim.Threshold == nil {
		return fmt.Errorf("score, salt, and threshold are required")
	}
	if claim.Score.Sign() < 0 || claim.Salt.Sign() < 0 || claim.Threshold.Sign() < 0 {
		return fmt.Errorf("score, salt, and threshold must be non-negative")
	}
	return nil
}
