package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

const localHardhatChainID int64 = 31337

type contractArtifact struct {
	ABI      json.RawMessage `json:"abi"`
	Bytecode string          `json:"bytecode"`
}

func validateRequest(request issueRequest) error {
	if strings.TrimSpace(request.LearnerID) == "" {
		return errors.New("learner ID is required")
	}
	if strings.TrimSpace(request.SkillID) == "" {
		return errors.New("skill ID is required")
	}
	if strings.TrimSpace(request.Evidence) == "" {
		return errors.New("evidence is required")
	}
	if request.Progress < 0 || request.Progress > 100 {
		return errors.New("progress must be from 0 to 100")
	}
	if strings.TrimSpace(request.Milestone) == "" {
		return errors.New("milestone is required")
	}
	if !common.IsHexAddress(request.HolderAddress) {
		return errors.New("holder address is required")
	}
	return nil
}

func commitment(kind, value string) common.Hash {
	return crypto.Keccak256Hash([]byte("skill-validation:mvp:" + kind + ":v1:" + value))
}

func findContractsDir() (string, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("read working directory: %w", err)
	}
	for {
		candidate := filepath.Join(workingDirectory, "contracts")
		if info, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil && !info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(workingDirectory)
		if parent == workingDirectory {
			return "", errors.New("could not find contracts/package.json from the current directory")
		}
		workingDirectory = parent
	}
}

func runNPM(ctx context.Context, directory string, arguments ...string) error {
	command := exec.CommandContext(ctx, "npm", arguments...)
	command.Dir = directory
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("npm %s: %w", strings.Join(arguments, " "), err)
	}
	return nil
}

func loadContractArtifact(path string) (abi.ABI, []byte, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return abi.ABI{}, nil, fmt.Errorf("read compiled contract artifact: %w", err)
	}
	var artifact contractArtifact
	if err := json.Unmarshal(contents, &artifact); err != nil {
		return abi.ABI{}, nil, fmt.Errorf("decode compiled contract artifact: %w", err)
	}
	parsedABI, err := abi.JSON(bytes.NewReader(artifact.ABI))
	if err != nil {
		return abi.ABI{}, nil, fmt.Errorf("parse contract ABI: %w", err)
	}
	bytecode := common.FromHex(artifact.Bytecode)
	if len(bytecode) == 0 {
		return abi.ABI{}, nil, errors.New("compiled contract bytecode is empty")
	}
	return parsedABI, bytecode, nil
}

func startHardhat(ctx context.Context, contractsDir string) (*rpc.Client, func(), error) {
	// Keep a stable address so a second terminal can issue and verify against
	// the same local chain. This command intentionally never starts a public RPC.
	listener, err := net.Listen("tcp", "127.0.0.1:8545")
	if err != nil {
		return nil, nil, fmt.Errorf("reserve local Hardhat port 8545: %w", err)
	}
	if err := listener.Close(); err != nil {
		return nil, nil, fmt.Errorf("release Hardhat port: %w", err)
	}

	command := exec.CommandContext(ctx, "npx", "hardhat", "node", "--hostname", "127.0.0.1", "--port", "8545")
	command.Dir = contractsDir
	// Hardhat prints its public development private keys at startup. The Go MVP
	// uses unlocked RPC accounts instead, so suppress that output completely.
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return nil, nil, fmt.Errorf("start Hardhat node: %w", err)
	}
	stop := func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_, _ = command.Process.Wait()
	}

	rpcURL := "http://127.0.0.1:8545"
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		client, dialErr := rpc.DialContext(ctx, rpcURL)
		if dialErr == nil {
			var chainID hexutil.Big
			chainErr := client.CallContext(ctx, &chainID, "eth_chainId")
			if chainErr == nil && chainID.ToInt().Int64() == localHardhatChainID {
				return client, stop, nil
			}
			client.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	stop()
	return nil, nil, errors.New("Hardhat node did not become ready on time")
}

func hardhatAccounts(ctx context.Context, client *rpc.Client) ([]common.Address, error) {
	var accounts []common.Address
	if err := client.CallContext(ctx, &accounts, "eth_accounts"); err != nil {
		return nil, fmt.Errorf("read Hardhat accounts: %w", err)
	}
	return accounts, nil
}

func waitReceipt(ctx context.Context, client *ethclient.Client, hash common.Hash) (*types.Receipt, error) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		receipt, err := client.TransactionReceipt(ctx, hash)
		if err == nil {
			if receipt.Status != types.ReceiptStatusSuccessful {
				return nil, fmt.Errorf("transaction %s reverted", hash)
			}
			return receipt, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("transaction %s was not mined on time", hash)
}

func callBool(ctx context.Context, client *ethclient.Client, address common.Address, contractABI abi.ABI, method string, arguments ...any) (bool, error) {
	data, err := contractABI.Pack(method, arguments...)
	if err != nil {
		return false, fmt.Errorf("encode %s call: %w", method, err)
	}
	output, err := client.CallContract(ctx, ethereumCallMessage(address, data), nil)
	if err != nil {
		return false, fmt.Errorf("call %s: %w", method, err)
	}
	decoded, err := contractABI.Unpack(method, output)
	if err != nil || len(decoded) != 1 {
		return false, fmt.Errorf("decode %s result: %w", method, err)
	}
	result, ok := decoded[0].(bool)
	if !ok {
		return false, fmt.Errorf("%s did not return a boolean", method)
	}
	return result, nil
}

func callHash(ctx context.Context, client *ethclient.Client, address common.Address, contractABI abi.ABI, method string, arguments ...any) (common.Hash, error) {
	data, err := contractABI.Pack(method, arguments...)
	if err != nil {
		return common.Hash{}, fmt.Errorf("encode %s call: %w", method, err)
	}
	output, err := client.CallContract(ctx, ethereumCallMessage(address, data), nil)
	if err != nil {
		return common.Hash{}, fmt.Errorf("call %s: %w", method, err)
	}
	decoded, err := contractABI.Unpack(method, output)
	if err != nil || len(decoded) != 1 {
		return common.Hash{}, fmt.Errorf("decode %s result: %w", method, err)
	}
	switch value := decoded[0].(type) {
	case [32]byte:
		return common.BytesToHash(value[:]), nil
	case common.Hash:
		return value, nil
	default:
		return common.Hash{}, fmt.Errorf("%s did not return a bytes32", method)
	}
}

func ethereumCallMessage(address common.Address, data []byte) ethereum.CallMsg {
	return ethereum.CallMsg{To: &address, Data: data}
}

func attestationID(subject, skill, program, evidence common.Hash) (common.Hash, error) {
	bytes32, err := abi.NewType("bytes32", "", nil)
	if err != nil {
		return common.Hash{}, err
	}
	encoded, err := abi.Arguments{{Type: bytes32}, {Type: bytes32}, {Type: bytes32}, {Type: bytes32}}.Pack(subject, skill, program, evidence)
	if err != nil {
		return common.Hash{}, fmt.Errorf("encode attestation ID: %w", err)
	}
	return crypto.Keccak256Hash(encoded), nil
}
