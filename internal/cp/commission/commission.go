// Package commission orchestrates the Commission step (#91, ADR-036): given an
// assigned device, it mints a per-device Tailscale key, gathers the device's
// ALPR license + the account PR token, and pushes the site-specific config to
// the device — cameras over the existing cameras.update command and the
// secrets over a commission command. Secret-bearing commands are non-retained
// (the publisher publishes with QoS-without-retain); the secrets are never
// logged here.
package commission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/emilejacobs/control-plane/internal/cp/registry"
	"github.com/emilejacobs/control-plane/internal/cp/tailscale"
	"github.com/emilejacobs/control-plane/internal/envelope"
	"github.com/emilejacobs/control-plane/internal/protocol/cameras"
	commissionproto "github.com/emilejacobs/control-plane/internal/protocol/commission"
)

// ErrNotAssigned is returned when commissioning a device with no site assigned
// — Assign must precede Commission (ADR-036).
var ErrNotAssigned = errors.New("commission: device is not assigned to a site")

// ErrNoPRToken is returned when a device has an ALPR license but no account-wide
// PR token is configured — the container would start half-configured.
var ErrNoPRToken = errors.New("commission: device has an ALPR license but no account PR token is set")

// DeviceStore is the registry surface Commission reads.
type DeviceStore interface {
	GetByID(ctx context.Context, id string) (registry.Device, error)
	GetALPRLicense(ctx context.Context, deviceID string) (string, error)
	GetCPSetting(ctx context.Context, key string) (string, bool, error)
	ListCameras(ctx context.Context, deviceID string) ([]cameras.Camera, error)
}

// Publisher publishes a command to a device's MQTT topic (non-retained).
type Publisher interface {
	Publish(ctx context.Context, topic string, payload []byte) error
}

// Config holds the Tailscale minting parameters.
type Config struct {
	Tailnet                string
	TailscaleTags          []string
	TailscaleExpirySeconds int
}

// Service performs Commission.
type Service struct {
	store  DeviceStore
	minter tailscale.Minter
	pub    Publisher
	cfg    Config
	newID  func() string
}

// New returns a Commission service.
func New(store DeviceStore, minter tailscale.Minter, pub Publisher, cfg Config, newID func() string) *Service {
	return &Service{store: store, minter: minter, pub: pub, cfg: cfg, newID: newID}
}

// Result reports what Commission did.
type Result struct {
	CorrelationID string
}

// Commission brings an assigned device into service. correlationID threads the
// request's id through both commands (empty → a fresh id is generated).
func (s *Service) Commission(ctx context.Context, deviceID, correlationID string) (Result, error) {
	dev, err := s.store.GetByID(ctx, deviceID)
	if err != nil {
		return Result{}, err
	}
	if dev.SiteID == nil {
		return Result{}, ErrNotAssigned
	}
	if correlationID == "" {
		correlationID = s.newID()
	}

	// Gather ALPR config first so we fail before minting a key / publishing if
	// the license-without-token invariant is violated.
	alpr, err := s.gatherALPR(ctx, deviceID)
	if err != nil {
		return Result{}, err
	}

	// Mint a per-device ephemeral single-use tagged key.
	key, err := s.minter.MintAuthKey(ctx, tailscale.MintOptions{
		Tags:          s.cfg.TailscaleTags,
		ExpirySeconds: s.cfg.TailscaleExpirySeconds,
		Description:   "uknomi device " + deviceID,
	})
	if err != nil {
		return Result{}, fmt.Errorf("mint tailscale key: %w", err)
	}

	// Push cameras over the existing cameras.update command.
	cams, err := s.store.ListCameras(ctx, deviceID)
	if err != nil {
		return Result{}, fmt.Errorf("list cameras: %w", err)
	}
	if cams == nil {
		cams = []cameras.Camera{}
	}
	camArgs, err := json.Marshal(cameras.UpdateAllRequest{Cameras: cams})
	if err != nil {
		return Result{}, fmt.Errorf("marshal cameras: %w", err)
	}
	if err := s.publish(ctx, deviceID, "cameras.update", camArgs, correlationID); err != nil {
		return Result{}, err
	}

	// Push the secrets over the commission command.
	commArgs, err := json.Marshal(commissionproto.Args{TailscaleAuthKey: key.Key, ALPR: alpr})
	if err != nil {
		return Result{}, fmt.Errorf("marshal commission args: %w", err)
	}
	if err := s.publish(ctx, deviceID, "commission", commArgs, correlationID); err != nil {
		return Result{}, err
	}

	return Result{CorrelationID: correlationID}, nil
}

func (s *Service) gatherALPR(ctx context.Context, deviceID string) (*commissionproto.ALPR, error) {
	license, err := s.store.GetALPRLicense(ctx, deviceID)
	if err != nil {
		return nil, fmt.Errorf("get alpr license: %w", err)
	}
	if license == "" {
		return nil, nil // not an ALPR device
	}
	token, ok, err := s.store.GetCPSetting(ctx, registry.SettingPlateRecognizerToken)
	if err != nil {
		return nil, fmt.Errorf("get pr token: %w", err)
	}
	if !ok || token == "" {
		return nil, ErrNoPRToken
	}
	return &commissionproto.ALPR{License: license, Token: token}, nil
}

func (s *Service) publish(ctx context.Context, deviceID, cmdType string, args json.RawMessage, correlationID string) error {
	cmd := envelope.Command{
		Type:          cmdType,
		CorrelationID: correlationID,
		CommandID:     s.newID(),
		Args:          args,
	}
	payload, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal %s command: %w", cmdType, err)
	}
	if err := s.pub.Publish(ctx, "devices/"+deviceID+"/cmd", payload); err != nil {
		return fmt.Errorf("publish %s: %w", cmdType, err)
	}
	return nil
}
