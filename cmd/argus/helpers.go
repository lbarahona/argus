package main

import (
	"fmt"

	"github.com/lbarahona/argus/internal/config"
	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/pkg/types"
)

// signozContext bundles what every Signoz-backed command needs: the loaded
// config, a ready-to-use client for the resolved instance, and the resolved
// instance's key and struct.
type signozContext struct {
	cfg     *types.Config
	client  *signoz.Client
	instKey string
	inst    *types.Instance
}

// newSignozContext loads config, resolves the instance (explicit flag value
// or configured default), and builds the client. It is the single path from
// flags to a Signoz client; resolution failures are returned, never
// swallowed.
func newSignozContext(instanceFlag string) (*signozContext, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	inst, instKey, err := config.GetInstance(cfg, instanceFlag)
	if err != nil {
		return nil, err
	}
	return &signozContext{
		cfg:     cfg,
		client:  signoz.New(*inst),
		instKey: instKey,
		inst:    inst,
	}, nil
}
