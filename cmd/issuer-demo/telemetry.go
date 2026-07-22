package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bperin/project-validation/internal/platform/observability"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type telemetryRequest struct {
	Registry    string
	Attestation string
	MetricName  string
	MetricValue float64
	Timestamp   int64
	Signature   string
}

type telemetryResult struct {
	Registry        string  `json:"registry"`
	AttestationID   string  `json:"attestationId"`
	MetricName      string  `json:"metricName"`
	MetricValue     float64 `json:"metricValue"`
	Timestamp       int64   `json:"timestamp"`
	Signature       string  `json:"signature,omitempty"`
	EvidenceBinding string  `json:"evidenceBinding"`
	Holder          string  `json:"holder,omitempty"`
	Verified        bool    `json:"verified"`
	Source          string  `json:"source,omitempty"`
}

func parseTelemetryFlags(arguments []string, requireSignature bool) (telemetryRequest, error) {
	flags := flag.NewFlagSet("telemetry", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	registry := flags.String("registry", "", "SkillMVPRegistry address")
	attestation := flags.String("attestation", "", "active skill attestation ID")
	metricName := flags.String("metric-name", "", "name of the reported metric (e.g. revenue_usd, active_users)")
	metricValue := flags.Float64("metric-value", 0, "numeric value of the metric")
	timestamp := flags.Int64("timestamp", 0, "Unix timestamp of the report (defaults to current time)")
	signature := flags.String("signature", "", "cryptographic holder signature")

	if err := flags.Parse(arguments); err != nil {
		return telemetryRequest{}, err
	}

	ts := *timestamp
	if ts == 0 {
		ts = time.Now().Unix()
	}

	req := telemetryRequest{
		Registry:    *registry,
		Attestation: *attestation,
		MetricName:  *metricName,
		MetricValue: *metricValue,
		Timestamp:   ts,
		Signature:   *signature,
	}

	if strings.TrimSpace(req.Registry) == "" || strings.TrimSpace(req.Attestation) == "" || strings.TrimSpace(req.MetricName) == "" {
		return telemetryRequest{}, errors.New("registry, attestation, and metric-name are required")
	}
	if requireSignature && strings.TrimSpace(req.Signature) == "" {
		return telemetryRequest{}, errors.New("signature is required for verification")
	}

	return req, nil
}

// telemetryEvidenceBinding computes the 32-byte hash representing the telemetry metrics payload
func telemetryEvidenceBinding(metricName string, metricValue float64, timestamp int64) common.Hash {
	payload := fmt.Sprintf("telemetry:%s:%f:%d", metricName, metricValue, timestamp)
	return crypto.Keccak256Hash([]byte(payload))
}

func signTelemetry(ctx context.Context, request telemetryRequest) (telemetryResult, error) {
	config, err := loadLocalConfig()
	if err != nil {
		return telemetryResult{}, err
	}
	if config.HolderKey == nil {
		return telemetryResult{}, errors.New("HARDHAT_HOLDER_PRIVATE_KEY is required for telemetry signing")
	}
	contractsDir, err := findContractsDir()
	if err != nil {
		return telemetryResult{}, err
	}
	contractABI, _, err := loadContractArtifact(artifactPath(contractsDir))
	if err != nil {
		return telemetryResult{}, err
	}
	client, rpcClient, _, err := connectHardhat(ctx, config.RPCURL)
	if err != nil {
		return telemetryResult{}, err
	}
	defer client.Close()
	defer rpcClient.Close()

	registry := common.HexToAddress(request.Registry)
	attestation := common.HexToHash(request.Attestation)
	evidence := telemetryEvidenceBinding(request.MetricName, request.MetricValue, request.Timestamp)

	// Fetch EIP-191 domain-bound project digest from the smart contract
	payload, err := callHash(ctx, client, registry, contractABI, "holderProjectDigest", attestation, evidence)
	if err != nil {
		return telemetryResult{}, err
	}

	// Sign the digest using the developer holder private key
	signature, err := crypto.Sign(accounts.TextHash(payload[:]), config.HolderKey)
	if err != nil {
		return telemetryResult{}, fmt.Errorf("sign telemetry metrics: %w", err)
	}
	signature[64] += 27 // Ethereum EIP-191 offset

	return telemetryResult{
		Registry:        registry.Hex(),
		AttestationID:   attestation.Hex(),
		MetricName:      request.MetricName,
		MetricValue:     request.MetricValue,
		Timestamp:       request.Timestamp,
		Signature:       hexutil.Encode(signature),
		EvidenceBinding: evidence.Hex(),
		Holder:          crypto.PubkeyToAddress(config.HolderKey.PublicKey).Hex(),
		Source:          "developer-holder-key",
	}, nil
}

func verifyTelemetry(ctx context.Context, request telemetryRequest) (telemetryResult, error) {
	rpcURL, err := loadLocalRPCURL()
	if err != nil {
		return telemetryResult{}, err
	}
	contractsDir, err := findContractsDir()
	if err != nil {
		return telemetryResult{}, err
	}
	contractABI, _, err := loadContractArtifact(artifactPath(contractsDir))
	if err != nil {
		return telemetryResult{}, err
	}
	client, rpcClient, _, err := connectHardhat(ctx, rpcURL)
	if err != nil {
		return telemetryResult{}, err
	}
	defer client.Close()
	defer rpcClient.Close()

	signature, err := hexutil.Decode(request.Signature)
	if err != nil {
		return telemetryResult{}, fmt.Errorf("decode signature: %w", err)
	}

	registry := common.HexToAddress(request.Registry)
	attestation := common.HexToHash(request.Attestation)
	evidence := telemetryEvidenceBinding(request.MetricName, request.MetricValue, request.Timestamp)

	// Verify on the Solidity contract that the recovered signer is the authorized holder of that attestation
	verified, err := callBool(ctx, client, registry, contractABI, "verifyHolderProject", attestation, evidence, signature)
	if err != nil {
		return telemetryResult{}, err
	}

	if verified {
		cfg := observability.Config{
			ServiceName: "issuer-demo-telemetry",
			Environment: "local",
			Exporter:    "console",
		}
		tel := observability.NewTelemetry(cfg)
		if err := tel.Start(ctx); err == nil {
			defer tel.Stop(ctx)

			meter := otel.Meter("skill-validation-metrics")
			gauge, err := meter.Float64Gauge(request.MetricName,
				metric.WithDescription("Cryptographically-authenticated learner success telemetry"),
			)
			if err == nil {
				gauge.Record(ctx, request.MetricValue, metric.WithAttributes(
					attribute.String("attestation_id", request.Attestation),
					attribute.String("registry", request.Registry),
					attribute.String("source", "solidity-contract"),
				))
			}
		}
	}

	return telemetryResult{
		Registry:        registry.Hex(),
		AttestationID:   attestation.Hex(),
		MetricName:      request.MetricName,
		MetricValue:     request.MetricValue,
		Timestamp:       request.Timestamp,
		EvidenceBinding: evidence.Hex(),
		Verified:        verified,
		Source:          "solidity-contract",
	}, nil
}
