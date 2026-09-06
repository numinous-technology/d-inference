package api

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/eigeninference/d-inference/coordinator/attestation"
	"github.com/eigeninference/d-inference/coordinator/protocol"
	"github.com/eigeninference/d-inference/coordinator/registry"
	"github.com/eigeninference/d-inference/coordinator/store"
)

// registerReconnectSession drives one registration + attestation verification
// against an already-populated store, mirroring the reconnect hot path without a
// live WebSocket. It returns the freshly registered provider.
func registerReconnectSession(t *testing.T, reg *registry.Registry, srv *Server, sessionID, publicKey string, blob []byte) *registry.Provider {
	t.Helper()
	msg := &protocol.RegisterMessage{Type: protocol.TypeRegister, PublicKey: publicKey,
		Attestation: blob, Version: minProviderVersionForReconnectAttestation}
	p := reg.Register(sessionID, nil, msg)
	srv.verifyProviderAttestation(sessionID, p, msg)
	return p
}

// TestProviderReconnectWithMissingReputationRow covers a provider whose prior
// record is present and SE-key-matched but has NO persisted reputation row (the
// row was pruned, or the machine never accumulated one). Restoration must not
// invent counters or fail — it recovers account linkage from the matched record
// and leaves reputation at the neutral cold-start default. Trust is still
// re-earned live.
func TestProviderReconnectWithMissingReputationRow(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory(store.Config{})
	publicKey := testPublicKeyB64()
	blob := createTestAttestationJSONWithSerial(t, "missing-rep-device", publicKey)
	identity, err := attestation.VerifyJSON(blob)
	if err != nil || !identity.Valid {
		t.Fatalf("invalid test attestation: %v, %+v", err, identity)
	}
	ctx := context.Background()
	// Prior session known by the attested SE key, with account linkage but no
	// reputation row.
	if err := st.UpsertProvider(ctx, store.ProviderRecord{
		ID: "prior-session", SerialNumber: "missing-rep-device",
		SEPublicKey: identity.PublicKey, AccountID: "owner-account",
		TrustLevel: "hardware", Attested: true, LastSeen: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	reg := registry.New(logger)
	srv := NewServer(reg, st, ServerConfig{}, logger)
	t.Cleanup(srv.Close)
	p := registerReconnectSession(t, reg, srv, "new-session", publicKey, blob)

	p.Mu().Lock()
	defer p.Mu().Unlock()
	if p.Reputation.TotalJobs != 0 || p.Reputation.SuccessfulJobs != 0 || p.Reputation.ChallengesPassed != 0 {
		t.Fatalf("missing reputation row should leave neutral defaults, got %+v", p.Reputation)
	}
	if p.AccountID != "owner-account" {
		t.Fatalf("account linkage not restored from SE-key match: %q", p.AccountID)
	}
	if p.TrustLevel != registry.TrustSelfSigned || p.MDAVerified {
		t.Fatalf("reconnect must re-earn hardware trust: trust=%s mda=%v", p.TrustLevel, p.MDAVerified)
	}
}

// TestProviderReconnectRepeatedRestoresLatestBySEKey covers a laptop that
// reconnects several times: each reconnect must recover the most recent standing
// bound to the attested SE key, and must ignore the currently registering
// session's own row so a just-persisted empty row cannot shadow the earned
// history.
func TestProviderReconnectRepeatedRestoresLatestBySEKey(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory(store.Config{})
	publicKey := testPublicKeyB64()
	blob := createTestAttestationJSONWithSerial(t, "repeat-device", publicKey)
	identity, err := attestation.VerifyJSON(blob)
	if err != nil || !identity.Valid {
		t.Fatalf("invalid test attestation: %v, %+v", err, identity)
	}
	ctx := context.Background()

	seed := func(id string, seenAgo time.Duration, jobs int) {
		if err := st.UpsertProvider(ctx, store.ProviderRecord{
			ID: id, SerialNumber: "repeat-device", SEPublicKey: identity.PublicKey,
			TrustLevel: "hardware", Attested: true, LastSeen: time.Now().Add(-seenAgo),
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertReputation(ctx, id, store.ReputationRecord{
			TotalJobs: jobs, SuccessfulJobs: jobs,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Two prior connections; the newer (session-2) carries the higher standing.
	seed("session-1", 2*time.Hour, 10)
	seed("session-2", 1*time.Hour, 40)

	reg := registry.New(logger)
	srv := NewServer(reg, st, ServerConfig{}, logger)
	t.Cleanup(srv.Close)

	// First reconnect: restores the latest-by-last_seen prior standing (40), not
	// the stale session-1 (10).
	p := registerReconnectSession(t, reg, srv, "reconnect-a", publicKey, blob)
	p.Mu().Lock()
	got := p.Reputation.TotalJobs
	p.Mu().Unlock()
	if got != 40 {
		t.Fatalf("first reconnect should restore latest standing 40, got %d", got)
	}

	// A newer prior session appears (the machine served more), plus a decoy row
	// under the very id the next registration will use — carrying an absurd
	// counter. The registering session must be excluded from its own lookup, so
	// the decoy must never win.
	seed("session-3", 30*time.Minute, 75)
	seed("reconnect-b", 0, 999)

	// Second reconnect: restores session-3 (75), excluding reconnect-b's own row.
	p2 := registerReconnectSession(t, reg, srv, "reconnect-b", publicKey, blob)
	p2.Mu().Lock()
	got2 := p2.Reputation.TotalJobs
	p2.Mu().Unlock()
	if got2 != 75 {
		t.Fatalf("second reconnect should restore latest prior standing 75 (excluding own row), got %d", got2)
	}
}
