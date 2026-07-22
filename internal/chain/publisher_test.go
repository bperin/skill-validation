package chain

import (
	"context"
	"errors"
	"math/big"
	"testing"
)

type fakeNetwork struct {
	chainID *big.Int
	err     error
}

func (f fakeNetwork) ChainID(context.Context) (*big.Int, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.chainID, nil
}

func TestVerifyArbitrumNetwork(t *testing.T) {
	t.Parallel()

	if err := VerifyNetwork(context.Background(), fakeNetwork{chainID: big.NewInt(ArbitrumOne.ChainID)}, ArbitrumOne); err != nil {
		t.Fatalf("VerifyNetwork() error = %v", err)
	}
}

func TestVerifyNetworkRejectsWrongChain(t *testing.T) {
	t.Parallel()

	err := VerifyNetwork(context.Background(), fakeNetwork{chainID: big.NewInt(421614)}, ArbitrumOne)
	if err == nil {
		t.Fatal("VerifyNetwork() error = nil, want wrong-chain error")
	}
}

func TestVerifyNetworkWrapsRPCError(t *testing.T) {
	t.Parallel()

	want := errors.New("rpc unavailable")
	err := VerifyNetwork(context.Background(), fakeNetwork{err: want}, ArbitrumOne)
	if !errors.Is(err, want) {
		t.Fatalf("VerifyNetwork() error = %v, want wrapped %v", err, want)
	}
}
