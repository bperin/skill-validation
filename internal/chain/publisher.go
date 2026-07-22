// Package chain provides the narrow EVM boundary used by attestation issuance.
package chain

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/ethclient"
)

type Network struct {
	Name    string
	ChainID int64
	RPCURL  string
}

// ArbitrumOne is the production EVM target. The URL is a public endpoint for
// connectivity only; deployments should use a dedicated provider endpoint.
var ArbitrumOne = Network{
	Name:    "arbitrum-one",
	ChainID: 42161,
	RPCURL:  "https://arb1.arbitrum.io/rpc",
}

type ChainIDClient interface {
	ChainID(context.Context) (*big.Int, error)
}

func VerifyNetwork(ctx context.Context, client ChainIDClient, network Network) error {
	if client == nil {
		return fmt.Errorf("chain ID client is required")
	}
	if strings.TrimSpace(network.Name) == "" || network.ChainID <= 0 {
		return fmt.Errorf("network name and positive chain ID are required")
	}
	actual, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("read %s chain ID: %w", network.Name, err)
	}
	if actual == nil || actual.Cmp(big.NewInt(network.ChainID)) != 0 {
		return fmt.Errorf("RPC chain ID %v does not match %s (%d)", actual, network.Name, network.ChainID)
	}
	return nil
}

// Dial verifies a configured RPC endpoint before it can be used for contract
// reads or signed transactions. It never broadcasts a transaction itself.
func Dial(ctx context.Context, rpcURL string, network Network) (*ethclient.Client, error) {
	if strings.TrimSpace(rpcURL) == "" {
		return nil, fmt.Errorf("RPC URL is required")
	}
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial %s RPC: %w", network.Name, err)
	}
	if err := VerifyNetwork(ctx, client, network); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}
