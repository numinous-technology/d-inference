package api

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/eigeninference/d-inference/coordinator/attestation"
	"github.com/eigeninference/d-inference/coordinator/registry"
	"github.com/eigeninference/d-inference/coordinator/store"
)

// TestProviderReconnectRestoresMetadataAndReputationIndependently is the
// regression for the review finding that a reputation-bearing OLD row was
// selected for ALL restored state, discarding a newer session's fresh device
// metadata. Device metadata (account linkage) must come from the newest row;
// reputation must come from the newest row that actually earned it. A reconnect
// therefore keeps its fresh account AND recovers the standing under its SE key.
func TestProviderReconnectRestoresMetadataAndReputationIndependently(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory(store.Config{})
	publicKey := testPublicKeyB64()
	blob := createTestAttestationJSONWithSerial(t, "meta-device", publicKey)
	identity, err := attestation.VerifyJSON(blob)
	if err != nil || !identity.Valid {
		t.Fatalf("invalid test attestation: %v, %+v", err, identity)
	}
	ctx := context.Background()

	// Older session earned standing under the account it was linked to then.
	if err := st.UpsertProvider(ctx, store.ProviderRecord{
		ID: "old-session", SerialNumber: "meta-device", SEPublicKey: identity.PublicKey,
		AccountID: "old-account", TrustLevel: "hardware", Attested: true,
		LastSeen: time.Now().Add(-2 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertReputation(ctx, "old-session", store.ReputationRecord{
		TotalJobs: 60, SuccessfulJobs: 58, FailedJobs: 2,
	}); err != nil {
		t.Fatal(err)
	}
	// Newer session re-linked the machine to a fresh account but has not yet
	// flushed a reputation row (bare metadata row).
	if err := st.UpsertProvider(ctx, store.ProviderRecord{
		ID: "new-session", SerialNumber: "meta-device", SEPublicKey: identity.PublicKey,
		AccountID: "new-account", TrustLevel: "hardware", Attested: true,
		LastSeen: time.Now().Add(-30 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	reg := registry.New(logger)
	srv := NewServer(reg, st, ServerConfig{}, logger)
	t.Cleanup(srv.Close)
	p := registerReconnectSession(t, reg, srv, "reconnect", publicKey, blob)

	p.Mu().Lock()
	defer p.Mu().Unlock()
	// Reputation is recovered from the earned (older) row...
	if p.Reputation.TotalJobs != 60 || p.Reputation.SuccessfulJobs != 58 || p.Reputation.FailedJobs != 2 {
		t.Fatalf("reputation should come from the earned row, got %+v", p.Reputation)
	}
	// ...while account linkage reflects the NEWEST device metadata, not the old
	// reputation-bearing row.
	if p.AccountID != "new-account" {
		t.Fatalf("account linkage should track newest metadata row, got %q", p.AccountID)
	}
	// Hardware trust is still re-earned live.
	if p.TrustLevel != registry.TrustSelfSigned || p.MDAVerified {
		t.Fatalf("reconnect must re-earn hardware trust: trust=%s mda=%v", p.TrustLevel, p.MDAVerified)
	}
}
