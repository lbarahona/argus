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

// renderOutput validates format and dispatches to the matching renderer.
// Pass nil for a renderer the command does not support; requesting an
// unsupported/unknown format is an error (not a silent default).
func renderOutput(format string, terminal func() error, markdown func() error, jsonValue any) error {
	switch format {
	case "", "terminal", "text", "table":
		if terminal == nil {
			return fmt.Errorf("terminal output not supported here")
		}
		return terminal()
	case "markdown", "md":
		if markdown == nil {
			return fmt.Errorf("markdown output is not supported by this command")
		}
		return markdown()
	case "json":
		if jsonValue == nil {
			return fmt.Errorf("json output is not supported by this command")
		}
		data, err := jsonMarshal(jsonValue)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	default:
		return fmt.Errorf("unknown format %q (valid: terminal, markdown, json)", format)
	}
}
