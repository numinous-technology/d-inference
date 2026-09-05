package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eigeninference/d-inference/coordinator/attestation"
	"github.com/eigeninference/d-inference/coordinator/protocol"
	"github.com/eigeninference/d-inference/coordinator/registry"
	"github.com/eigeninference/d-inference/coordinator/store"
	"nhooyr.io/websocket"
)

func TestProviderReconnectRestoresReputationEarnedAfterStartup(t *testing.T) {
	for _, serial := range []string{"reconnect-device", ""} {
		name := "with_serial"
		if serial == "" {
			name = "without_serial"
		}
		t.Run(name, func(t *testing.T) { testProviderReconnectRestoresReputation(t, serial) })
	}
}

func testProviderReconnectRestoresReputation(t *testing.T, serial string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory(store.Config{})
	reg := registry.New(logger)
	srv := NewServer(reg, st, ServerConfig{}, logger)
	t.Cleanup(srv.Close)
	publicKey := testPublicKeyB64()
	blob := createTestAttestationJSONWithSerial(t, serial, publicKey)
	identity, err := attestation.VerifyJSON(blob)
	if err != nil || !identity.Valid {
		t.Fatalf("invalid test attestation: %v, %+v", err, identity)
	}

	// This session and its successful work did not exist in the startup snapshot.
	ctx := context.Background()
	if err := st.UpsertProvider(ctx, store.ProviderRecord{
		ID: "previous-session", SerialNumber: "reconnect-device",
		SEPublicKey: identity.PublicKey, TrustLevel: "hardware", Attested: true,
		LastSeen: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	want := store.ReputationRecord{TotalJobs: 52, SuccessfulJobs: 50, FailedJobs: 2,
		TotalUptimeSeconds: 3600, AvgResponseTimeMs: 120, ChallengesPassed: 12, ChallengesFailed: 1}
	if err := st.UpsertReputation(ctx, "previous-session", want); err != nil {
		t.Fatal(err)
	}
	msg := &protocol.RegisterMessage{Type: protocol.TypeRegister, PublicKey: publicKey,
		Attestation: blob, Version: minProviderVersionForReconnectAttestation,
		Backend: "mlx-swift", Models: []protocol.ModelInfo{{ID: "reconnect-model"}}}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	wsCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(wsCtx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/provider", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(wsCtx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
	// desired_models is sent after registration verification and state restoration.
	for {
		_, data, err := conn.Read(wsCtx)
		if err != nil {
			t.Fatal(err)
		}
		var message struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type == "desired_models" {
			break
		}
	}
	p := findProviderByModel(reg, "reconnect-model")
	if p == nil {
		t.Fatal("registered provider missing")
	}
	p.Mu().Lock()
	defer p.Mu().Unlock()
	if p.Reputation.TotalJobs != want.TotalJobs || p.Reputation.SuccessfulJobs != want.SuccessfulJobs ||
		p.Reputation.FailedJobs != want.FailedJobs || p.Reputation.TotalUptime != time.Hour ||
		p.Reputation.AvgResponseTime != 120*time.Millisecond || p.Reputation.ChallengesPassed != 12 ||
		p.Reputation.ChallengesFailed != 1 {
		t.Fatalf("reconnect lost persisted reputation: got %+v, want %+v", p.Reputation, want)
	}
	if p.TrustLevel != registry.TrustSelfSigned || p.MDAVerified {
		t.Fatalf("reconnect must re-earn hardware trust: trust=%s mda=%v", p.TrustLevel, p.MDAVerified)
	}
}

func TestProviderReconnectDoesNotRestoreAnotherSEKey(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory(store.Config{})
	publicKey := testPublicKeyB64()
	blob := createTestAttestationJSONWithSerial(t, "shared-serial", publicKey)
	ctx := context.Background()
	if err := st.UpsertProvider(ctx, store.ProviderRecord{
		ID: "other-session", SEPublicKey: "different-signing-key", SerialNumber: "shared-serial",
		AccountID: "other-account", TrustLevel: "hardware", LastSeen: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertReputation(ctx, "other-session", store.ReputationRecord{TotalJobs: 99, SuccessfulJobs: 99}); err != nil {
		t.Fatal(err)
	}
	reg := registry.New(logger)
	srv := NewServer(reg, st, ServerConfig{}, logger)
	t.Cleanup(srv.Close)
	msg := &protocol.RegisterMessage{Type: protocol.TypeRegister, PublicKey: publicKey,
		Attestation: blob, Version: minProviderVersionForReconnectAttestation}
	p := reg.Register("new-session", nil, msg)
	srv.verifyProviderAttestation(p.ID, p, msg)
	p.Mu().Lock()
	defer p.Mu().Unlock()
	if p.Reputation.TotalJobs != 0 || p.AccountID != "" {
		t.Fatalf("different SE key inherited state: reputation=%+v account=%q", p.Reputation, p.AccountID)
	}
}
