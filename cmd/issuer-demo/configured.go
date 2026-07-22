package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/joho/godotenv"
)

type localConfig struct {
	RPCURL    string
	RootKey   *ecdsa.PrivateKey
	HolderKey *ecdsa.PrivateKey
}

type verifyRequest struct {
	Registry  string
	LearnerID string
	SkillID   string
}

type upgradeRequest struct {
	Registry      string
	Predecessor   string
	SkillID       string
	Evidence      string
	Progress      int
	Milestone     string
	HolderAddress string
}

type projectProofRequest struct {
	Registry    string
	Attestation string
	Evidence    string
	Signature   string
}

type issuedResult struct {
	Network           string         `json:"network"`
	ChainID           int64          `json:"chainId"`
	Registry          common.Address `json:"registry"`
	RootIssuer        common.Address `json:"rootIssuer"`
	ChildIssuer       common.Address `json:"childIssuer"`
	Holder            common.Address `json:"holder"`
	LearnerCommitment common.Hash    `json:"learnerCommitment"`
	SkillCommitment   common.Hash    `json:"skillCommitment"`
	EvidenceBinding   common.Hash    `json:"evidenceBinding"`
	AttestationID     common.Hash    `json:"attestationId"`
	Progress          int            `json:"progress"`
	Milestone         string         `json:"milestone"`
	IssuerName        string         `json:"issuerName"`
}

type verificationResult struct {
	Source        string         `json:"source"`
	Registry      common.Address `json:"registry"`
	AttestationID common.Hash    `json:"attestationId"`
	HasSkill      bool           `json:"hasSkill"`
	Current       bool           `json:"current"`
	IssuerName        string         `json:"issuerName"`
}

type upgradeResult struct {
	Registry        common.Address `json:"registry"`
	Predecessor     common.Hash    `json:"predecessor"`
	Successor       common.Hash    `json:"successor"`
	EvidenceBinding common.Hash    `json:"evidenceBinding"`
	Progress        int            `json:"progress"`
	Milestone       string         `json:"milestone"`
	Current         bool           `json:"current"`
}

type projectProofResult struct {
	Registry        common.Address `json:"registry"`
	AttestationID   common.Hash    `json:"attestationId"`
	EvidenceBinding common.Hash    `json:"evidenceBinding"`
	Holder          common.Address `json:"holder,omitempty"`
	Signature       string         `json:"signature,omitempty"`
	Verified        bool           `json:"verified"`
	Source          string         `json:"source"`
}

func runNetwork(ctx context.Context) error {
	contractsDir, err := findContractsDir()
	if err != nil {
		return err
	}
	if err := runNPM(ctx, contractsDir, "run", "compile"); err != nil {
		return err
	}
	client, stop, err := startHardhat(ctx, contractsDir)
	if err != nil {
		return err
	}
	defer stop()
	defer client.Close()
	accounts, err := hardhatAccounts(ctx, client)
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return errors.New("Hardhat returned no accounts")
	}
	fmt.Fprintln(os.Stderr, "Hardhat local network ready")
	fmt.Fprintln(os.Stderr, "RPC: http://127.0.0.1:8545")
	fmt.Fprintln(os.Stderr, "Chain ID: 31337")
	fmt.Fprintln(os.Stderr, "Root account:", accounts[0].Hex())
	fmt.Fprintln(os.Stderr, "Press Ctrl C to stop the local network.")
	<-ctx.Done()
	return nil
}

func issueOnHardhat(ctx context.Context, request issueRequest) (issuedResult, error) {
	if err := validateRequest(request); err != nil {
		return issuedResult{}, err
	}
	config, err := loadLocalConfig()
	if err != nil {
		return issuedResult{}, err
	}
	contractsDir, err := findContractsDir()
	if err != nil {
		return issuedResult{}, err
	}
	if err := runNPM(ctx, contractsDir, "run", "compile"); err != nil {
		return issuedResult{}, err
	}
	contractABI, bytecode, err := loadContractArtifact(artifactPath(contractsDir))
	if err != nil {
		return issuedResult{}, err
	}
	client, rpcClient, chainID, err := connectHardhat(ctx, config.RPCURL)
	if err != nil {
		return issuedResult{}, err
	}
	defer client.Close()
	defer rpcClient.Close()

	root := crypto.PubkeyToAddress(config.RootKey.PublicKey)
	if err := requireFundedAccount(ctx, client, root); err != nil {
		return issuedResult{}, err
	}
	childKey, err := crypto.GenerateKey()
	if err != nil {
		return issuedResult{}, fmt.Errorf("generate local child issuer key: %w", err)
	}
	child := crypto.PubkeyToAddress(childKey.PublicKey)
	holder := common.HexToAddress(request.HolderAddress)

	registry, err := deployWithSigner(ctx, client, contractABI, bytecode, config.RootKey, chainID)
	if err != nil {
		return issuedResult{}, err
	}
	if err := fundChild(ctx, client, config.RootKey, child, chainID); err != nil {
		return issuedResult{}, err
	}

	subject := commitment("subject", request.LearnerID)
	skill := commitment("skill", request.SkillID)
	program := commitment("program", request.SkillID+"-v1")
	evidence := evidenceCommitment(request.Evidence, request.Progress, request.Milestone)
	expiresAt := uint64(time.Now().Add(90 * 24 * time.Hour).Unix())
	if _, err := transactWithSigner(ctx, client, registry, contractABI, config.RootKey, chainID, "delegateIssuerFor", child, subject, skill, program, expiresAt); err != nil {
		return issuedResult{}, fmt.Errorf("delegate issuer child: %w", err)
	}
	if _, err := transactWithSigner(ctx, client, registry, contractABI, childKey, chainID, "attest", subject, skill, program, evidence); err != nil {
		return issuedResult{}, fmt.Errorf("attest skill: %w", err)
	}
	attestation, err := attestationID(subject, skill, program, evidence)
	if err != nil {
		return issuedResult{}, err
	}
	if _, err := transactWithSigner(ctx, client, registry, contractABI, childKey, chainID, "bindHolder", attestation, holder, expiresAt); err != nil {
		return issuedResult{}, fmt.Errorf("bind holder: %w", err)
	}

	issuerName := strings.TrimSpace(os.Getenv("SKILL_ISSUER_NAME"))
	if issuerName == "" {
		issuerName = "BrianSkillCo"
	}

	return issuedResult{
		Network:           "hardhat",
		ChainID:           chainID.Int64(),
		Registry:          registry,
		RootIssuer:        root,
		ChildIssuer:       child,
		Holder:            holder,
		LearnerCommitment: subject,
		SkillCommitment:   skill,
		EvidenceBinding:   evidence,
		AttestationID:     attestation,
		Progress:          request.Progress,
		Milestone:         request.Milestone,
		IssuerName:        issuerName,
	}, nil
}

func verifyOnHardhat(ctx context.Context, request verifyRequest) (verificationResult, error) {
	if strings.TrimSpace(request.Registry) == "" || strings.TrimSpace(request.LearnerID) == "" || strings.TrimSpace(request.SkillID) == "" {
		return verificationResult{}, errors.New("registry, learner ID, and skill ID are required")
	}
	rpcURL, err := loadLocalRPCURL()
	if err != nil {
		return verificationResult{}, err
	}
	contractsDir, err := findContractsDir()
	if err != nil {
		return verificationResult{}, err
	}
	contractABI, _, err := loadContractArtifact(artifactPath(contractsDir))
	if err != nil {
		return verificationResult{}, err
	}
	client, rpcClient, _, err := connectHardhat(ctx, rpcURL)
	if err != nil {
		return verificationResult{}, err
	}
	defer client.Close()
	defer rpcClient.Close()
	registry := common.HexToAddress(request.Registry)
	subject := commitment("subject", request.LearnerID)
	skill := commitment("skill", request.SkillID)
	hasSkill, err := callBool(ctx, client, registry, contractABI, "hasSkill", subject, skill)
	if err != nil {
		return verificationResult{}, err
	}
	id, err := callHash(ctx, client, registry, contractABI, "currentAttestationId", subject, skill)
	if err != nil {
		return verificationResult{}, err
	}
	current := false
	if id != (common.Hash{}) {
		current, err = callBool(ctx, client, registry, contractABI, "isCurrent", id)
		if err != nil {
			return verificationResult{}, err
		}
	}
	issuerName := strings.TrimSpace(os.Getenv("SKILL_ISSUER_NAME"))
	if issuerName == "" {
		issuerName = "BrianSkillCo"
	}
	return verificationResult{Source: "solidity-contract", Registry: registry, AttestationID: id, HasSkill: hasSkill, Current: current, IssuerName: issuerName}, nil
}

func signProject(ctx context.Context, request projectProofRequest) (projectProofResult, error) {
	if err := validateProjectProofRequest(request); err != nil {
		return projectProofResult{}, err
	}
	config, err := loadLocalConfig()
	if err != nil {
		return projectProofResult{}, err
	}
	if config.HolderKey == nil {
		return projectProofResult{}, errors.New("HARDHAT_HOLDER_PRIVATE_KEY is required for local project signing")
	}
	contractsDir, err := findContractsDir()
	if err != nil {
		return projectProofResult{}, err
	}
	contractABI, _, err := loadContractArtifact(artifactPath(contractsDir))
	if err != nil {
		return projectProofResult{}, err
	}
	client, rpcClient, _, err := connectHardhat(ctx, config.RPCURL)
	if err != nil {
		return projectProofResult{}, err
	}
	defer client.Close()
	defer rpcClient.Close()
	registry := common.HexToAddress(request.Registry)
	attestation := common.HexToHash(request.Attestation)
	evidence := holderEvidenceCommitment(request.Evidence)
	payload, err := callHash(ctx, client, registry, contractABI, "holderProjectDigest", attestation, evidence)
	if err != nil {
		return projectProofResult{}, err
	}
	signature, err := crypto.Sign(accounts.TextHash(payload[:]), config.HolderKey)
	if err != nil {
		return projectProofResult{}, fmt.Errorf("sign project proof: %w", err)
	}
	// go-ethereum returns recovery IDs 0/1. Solidity's ecrecover and wallet
	// tooling use the externally serialized EIP-191 form 27/28.
	signature[64] += 27
	return projectProofResult{
		Registry: registry, AttestationID: attestation, EvidenceBinding: evidence,
		Holder: crypto.PubkeyToAddress(config.HolderKey.PublicKey), Signature: hexutil.Encode(signature), Source: "developer-holder-key",
	}, nil
}

func verifyProject(ctx context.Context, request projectProofRequest) (projectProofResult, error) {
	if err := validateProjectProofRequest(request); err != nil || strings.TrimSpace(request.Signature) == "" {
		if err != nil {
			return projectProofResult{}, err
		}
		return projectProofResult{}, errors.New("signature is required")
	}
	rpcURL, err := loadLocalRPCURL()
	if err != nil {
		return projectProofResult{}, err
	}
	contractsDir, err := findContractsDir()
	if err != nil {
		return projectProofResult{}, err
	}
	contractABI, _, err := loadContractArtifact(artifactPath(contractsDir))
	if err != nil {
		return projectProofResult{}, err
	}
	client, rpcClient, _, err := connectHardhat(ctx, rpcURL)
	if err != nil {
		return projectProofResult{}, err
	}
	defer client.Close()
	defer rpcClient.Close()
	signature, err := hexutil.Decode(request.Signature)
	if err != nil {
		return projectProofResult{}, fmt.Errorf("decode signature: %w", err)
	}
	registry := common.HexToAddress(request.Registry)
	attestation := common.HexToHash(request.Attestation)
	evidence := holderEvidenceCommitment(request.Evidence)
	verified, err := callBool(ctx, client, registry, contractABI, "verifyHolderProject", attestation, evidence, signature)
	if err != nil {
		return projectProofResult{}, err
	}
	return projectProofResult{Registry: registry, AttestationID: attestation, EvidenceBinding: evidence, Verified: verified, Source: "solidity-contract"}, nil
}

func upgradeOnHardhat(ctx context.Context, request upgradeRequest) (upgradeResult, error) {
	if strings.TrimSpace(request.Registry) == "" || strings.TrimSpace(request.Predecessor) == "" || strings.TrimSpace(request.SkillID) == "" || strings.TrimSpace(request.Evidence) == "" {
		return upgradeResult{}, errors.New("registry, predecessor, skill ID, and evidence are required")
	}
	if request.Progress < 0 || request.Progress > 100 || strings.TrimSpace(request.Milestone) == "" {
		return upgradeResult{}, errors.New("progress must be from 0 to 100 and milestone is required")
	}
	config, err := loadLocalConfig()
	if err != nil {
		return upgradeResult{}, err
	}
	contractsDir, err := findContractsDir()
	if err != nil {
		return upgradeResult{}, err
	}
	contractABI, _, err := loadContractArtifact(artifactPath(contractsDir))
	if err != nil {
		return upgradeResult{}, err
	}
	client, rpcClient, chainID, err := connectHardhat(ctx, config.RPCURL)
	if err != nil {
		return upgradeResult{}, err
	}
	defer client.Close()
	defer rpcClient.Close()
	registry := common.HexToAddress(request.Registry)
	predecessor := common.HexToHash(request.Predecessor)
	program := commitment("program", request.SkillID+"-v2")
	evidence := evidenceCommitment(request.Evidence, request.Progress, request.Milestone)
	if _, err := transactWithSigner(ctx, client, registry, contractABI, config.RootKey, chainID, "supersede", predecessor, program, evidence); err != nil {
		return upgradeResult{}, fmt.Errorf("supersede skill: %w", err)
	}
	successor, err := callHash(ctx, client, registry, contractABI, "successorOf", predecessor)
	if err != nil {
		return upgradeResult{}, err
	}
	if request.HolderAddress != "" {
		holder := common.HexToAddress(request.HolderAddress)
		expiresAt := uint64(time.Now().Add(90 * 24 * time.Hour).Unix())
		if _, err := transactWithSigner(ctx, client, registry, contractABI, config.RootKey, chainID, "bindHolder", successor, holder, expiresAt); err != nil {
			return upgradeResult{}, fmt.Errorf("bind holder on successor: %w", err)
		}
	}
	current, err := callBool(ctx, client, registry, contractABI, "isCurrent", successor)
	if err != nil {
		return upgradeResult{}, err
	}
	return upgradeResult{Registry: registry, Predecessor: predecessor, Successor: successor, EvidenceBinding: evidence, Progress: request.Progress, Milestone: request.Milestone, Current: current}, nil
}

func loadLocalConfig() (localConfig, error) {
	_ = godotenv.Load()
	rpcURL, err := loadLocalRPCURL()
	if err != nil {
		return localConfig{}, err
	}
	keyValue := strings.TrimSpace(os.Getenv("HARDHAT_ROOT_PRIVATE_KEY"))
	if keyValue == "" {
		return localConfig{}, errors.New("copy .env.example to .env and configure local Hardhat values")
	}
	key, err := crypto.HexToECDSA(strings.TrimPrefix(keyValue, "0x"))
	if err != nil {
		return localConfig{}, fmt.Errorf("parse HARDHAT_ROOT_PRIVATE_KEY: %w", err)
	}
	var holderKey *ecdsa.PrivateKey
	if holderValue := strings.TrimSpace(os.Getenv("HARDHAT_HOLDER_PRIVATE_KEY")); holderValue != "" {
		holderKey, err = crypto.HexToECDSA(strings.TrimPrefix(holderValue, "0x"))
		if err != nil {
			return localConfig{}, fmt.Errorf("parse HARDHAT_HOLDER_PRIVATE_KEY: %w", err)
		}
	}
	return localConfig{RPCURL: rpcURL, RootKey: key, HolderKey: holderKey}, nil
}

func loadLocalRPCURL() (string, error) {
	_ = godotenv.Load()
	rpcURL := strings.TrimSpace(os.Getenv("HARDHAT_RPC_URL"))
	if rpcURL == "" {
		return "", errors.New("copy .env.example to .env and configure HARDHAT_RPC_URL")
	}
	return rpcURL, nil
}

func validateProjectProofRequest(request projectProofRequest) error {
	if !common.IsHexAddress(request.Registry) || common.HexToAddress(request.Registry) == (common.Address{}) {
		return errors.New("valid registry address is required")
	}
	if common.HexToHash(request.Attestation) == (common.Hash{}) || strings.TrimSpace(request.Evidence) == "" {
		return errors.New("attestation ID and evidence are required")
	}
	return nil
}

func connectHardhat(ctx context.Context, rpcURL string) (*ethclient.Client, *rpc.Client, *big.Int, error) {
	rpcClient, err := rpc.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dial local Hardhat RPC: %w", err)
	}
	client := ethclient.NewClient(rpcClient)
	chainID, err := client.ChainID(ctx)
	if err != nil {
		client.Close()
		rpcClient.Close()
		return nil, nil, nil, fmt.Errorf("read Hardhat chain ID: %w", err)
	}
	if chainID.Int64() != localHardhatChainID {
		client.Close()
		rpcClient.Close()
		return nil, nil, nil, fmt.Errorf("refusing configured key on chain %s; local Hardhat requires %d", chainID, localHardhatChainID)
	}
	return client, rpcClient, chainID, nil
}

func requireFundedAccount(ctx context.Context, client *ethclient.Client, address common.Address) error {
	balance, err := client.BalanceAt(ctx, address, nil)
	if err != nil {
		return err
	}
	if balance.Sign() <= 0 {
		return fmt.Errorf("configured root account %s has no local Hardhat ETH", address)
	}
	return nil
}

func deployWithSigner(ctx context.Context, client *ethclient.Client, contractABI abi.ABI, bytecode []byte, key *ecdsa.PrivateKey, chainID *big.Int) (common.Address, error) {
	auth, err := bind.NewKeyedTransactorWithChainID(key, chainID)
	if err != nil {
		return common.Address{}, err
	}
	address, transaction, _, err := bind.DeployContract(auth, contractABI, bytecode, client)
	if err != nil {
		return common.Address{}, err
	}
	if _, err := waitReceipt(ctx, client, transaction.Hash()); err != nil {
		return common.Address{}, err
	}
	return address, nil
}

func transactWithSigner(ctx context.Context, client *ethclient.Client, address common.Address, contractABI abi.ABI, key *ecdsa.PrivateKey, chainID *big.Int, method string, arguments ...any) (*types.Transaction, error) {
	auth, err := bind.NewKeyedTransactorWithChainID(key, chainID)
	if err != nil {
		return nil, err
	}
	bound := bind.NewBoundContract(address, contractABI, client, client, client)
	transaction, err := bound.Transact(auth, method, arguments...)
	if err != nil {
		return nil, err
	}
	if _, err := waitReceipt(ctx, client, transaction.Hash()); err != nil {
		return nil, err
	}
	return transaction, nil
}

func fundChild(ctx context.Context, client *ethclient.Client, key *ecdsa.PrivateKey, child common.Address, chainID *big.Int) error {
	root := crypto.PubkeyToAddress(key.PublicKey)
	nonce, err := client.PendingNonceAt(ctx, root)
	if err != nil {
		return err
	}
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return err
	}
	transaction := types.NewTx(&types.LegacyTx{Nonce: nonce, To: &child, Value: big.NewInt(1_000_000_000_000_000), Gas: 21_000, GasPrice: gasPrice})
	signed, err := types.SignTx(transaction, types.LatestSignerForChainID(chainID), key)
	if err != nil {
		return err
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		return err
	}
	_, err = waitReceipt(ctx, client, signed.Hash())
	return err
}

func evidenceCommitment(evidence string, progress int, milestone string) common.Hash {
	if strings.HasPrefix(evidence, "0x") && len(evidence) == 66 {
		if _, err := hexutil.Decode(evidence); err == nil {
			return common.HexToHash(evidence)
		}
	}
	payload, _ := json.Marshal(struct {
		Evidence  string `json:"evidence"`
		Progress  int    `json:"progress"`
		Milestone string `json:"milestone"`
	}{Evidence: evidence, Progress: progress, Milestone: milestone})
	return commitment("evidence", string(payload))
}

func holderEvidenceCommitment(evidence string) common.Hash {
	if strings.HasPrefix(evidence, "0x") && len(evidence) == 66 {
		if _, err := hexutil.Decode(evidence); err == nil {
			return common.HexToHash(evidence)
		}
	}
	return commitment("evidence", evidence)
}

func artifactPath(contractsDir string) string {
	return filepath.Join(contractsDir, "artifacts", "src", "SkillMVPRegistry.sol", "SkillMVPRegistry.json")
}
