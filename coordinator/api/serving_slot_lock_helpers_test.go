package api

import (
	"testing"
	"time"

	"github.com/eigeninference/d-inference/coordinator/protocol"
	"github.com/eigeninference/d-inference/coordinator/registry"
)

// Helper-level regressions for the first-byte serving-slot lock dependency.
//
// noteServingSlotFor / dispatchState.kvBackendAttribution run between the
// inference-frame handoff and the first client byte. Before the fix they
// resolved the slot's KV backend through Registry.SlotKVBackendTags ->
// Registry.GetProvider, which takes the registry READ lock, so a concurrent
// registry WRITE lock (a heartbeat/registration burst) stalled the first byte.
// The fix resolves from the *registry.Provider the dispatch goroutine already
// holds (Provider.SlotKVBackendTags, provider lock only). These tests hold the
// registry write lock and assert each helper completes anyway — with the old
// registry-backed methods each blocks for the whole lock hold.

// seedKVBackend registers a provider and drives one heartbeat so the slot has a
// concrete kv_backend observation (paged/contiguous), returning the provider.
func seedKVBackend(t *testing.T, reg *registry.Registry, id, model, backend string) *registry.Provider {
	t.Helper()
	p := reg.Register(id, nil, &protocol.RegisterMessage{
		Models: []protocol.ModelInfo{{ID: model, ModelType: "chat", Quantization: "4bit"}},
	})
	kb := backend
	reg.Heartbeat(id, &protocol.HeartbeatMessage{
		Type:   protocol.TypeHeartbeat,
		Status: "serving",
		BackendCapacity: &protocol.BackendCapacity{
			TotalMemoryGB: 64,
			Slots: []protocol.BackendSlotCapacity{
				{Model: model, State: "running", KVBackend: &kb},
			},
		},
	})
	return p
}

// runWhileWriteLockHeld runs fn on a goroutine while the registry write lock is
// held and asserts it finishes inside a bounded window. A helper that reaches
// the registry read lock would block here for the whole hold; the bounded
// channel wait makes that a deterministic failure rather than a hang, and the
// deferred release never leaves a goroutine or the registry wedged.
func runWhileWriteLockHeld(t *testing.T, reg *registry.Registry, fn func()) {
	t.Helper()
	release := reg.HoldWriteLockForTest()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
		release()
	case <-time.After(time.Second):
		// Do not leave the blocked goroutine wedged behind the lock: releasing
		// lets it drain before the test exits.
		release()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("helper remained blocked after the registry lock was released")
		}
		t.Fatal("helper blocked on the registry lock between handoff and first byte")
	}
}

// TestNoteServingSlotForPrimaryNoRegistryLock covers the primary handoff choke
// point (dispatchPrimary -> noteServingSlotFor(d.provider, d.pr)).
func TestNoteServingSlotForPrimaryNoRegistryLock(t *testing.T) {
	srv, reg, _, ts := setupTestServer(t)
	defer ts.Close()

	const model = "primary-model"
	provider := seedKVBackend(t, reg, "primary-provider", model, registry.KVBackendPaged)
	pr := &registry.PendingRequest{RequestID: "r1", Model: model, ProviderID: provider.ID}
	d := &dispatchState{s: srv, model: model, provider: provider, pr: pr}

	runWhileWriteLockHeld(t, reg, func() { d.noteServingSlotFor(provider, pr) })

	if d.servedKVSlot.providerID != provider.ID {
		t.Fatalf("latched providerID = %q, want %q", d.servedKVSlot.providerID, provider.ID)
	}
	if d.servedKVSlot.backend.Backend != registry.KVBackendPaged {
		t.Fatalf("latched backend = %q, want paged", d.servedKVSlot.backend.Backend)
	}
}

// TestNoteServingSlotForBackupPointerNoRegistryLock covers the backup pointer
// path (racePrimaryFailedWaitBackup -> noteServingSlotFor(backupProvider,
// backupPR)): the latch must re-point to the backup's slot, from the backup
// provider already in hand.
func TestNoteServingSlotForBackupPointerNoRegistryLock(t *testing.T) {
	srv, reg, _, ts := setupTestServer(t)
	defer ts.Close()

	const model = "backup-model"
	primary := seedKVBackend(t, reg, "backup-primary", model, registry.KVBackendPaged)
	backup := seedKVBackend(t, reg, "backup-secondary", model, registry.KVBackendContiguous)

	primaryPR := &registry.PendingRequest{RequestID: "rp", Model: model, ProviderID: primary.ID}
	d := &dispatchState{s: srv, model: model, provider: primary, pr: primaryPR}
	// Primary latched first.
	d.noteServingSlotFor(primary, primaryPR)

	backupPR := &registry.PendingRequest{RequestID: "rb", Model: model, ProviderID: backup.ID}
	runWhileWriteLockHeld(t, reg, func() { d.noteServingSlotFor(backup, backupPR) })

	if d.servedKVSlot.providerID != backup.ID {
		t.Fatalf("latched providerID = %q, want backup %q", d.servedKVSlot.providerID, backup.ID)
	}
	if d.servedKVSlot.backend.Backend != registry.KVBackendContiguous {
		t.Fatalf("latched backend = %q, want contiguous", d.servedKVSlot.backend.Backend)
	}
}

// TestKVBackendAttributionPromotedLatchMismatchNoRegistryLock covers the
// promoted-winner / latch-mismatch branch of dispatchState.kvBackendAttribution:
// the live d.pr names a provider the latch has not caught up with (a backup
// promoted outside the noteServingSlot choke point). It must resolve — and
// re-latch — to the promoted provider without the registry lock.
func TestKVBackendAttributionPromotedLatchMismatchNoRegistryLock(t *testing.T) {
	srv, reg, _, ts := setupTestServer(t)
	defer ts.Close()

	const model = "promoted-model"
	primary := seedKVBackend(t, reg, "promoted-primary", model, registry.KVBackendPaged)
	winner := seedKVBackend(t, reg, "promoted-winner", model, registry.KVBackendContiguous)

	primaryPR := &registry.PendingRequest{RequestID: "rp", Model: model, ProviderID: primary.ID}
	d := &dispatchState{s: srv, model: model, provider: primary, pr: primaryPR}
	d.noteServingSlotFor(primary, primaryPR) // latch names the primary

	// Promotion: d.provider/d.pr now point at the winner, latch still on primary.
	winnerPR := &registry.PendingRequest{RequestID: "rw", Model: model, ProviderID: winner.ID}
	d.provider = winner
	d.pr = winnerPR

	var got kvBackendAttribution
	runWhileWriteLockHeld(t, reg, func() { got = d.kvBackendAttribution() })

	if got.Backend != registry.KVBackendContiguous {
		t.Fatalf("attribution backend = %q, want contiguous (the promoted winner)", got.Backend)
	}
	if d.servedKVSlot.providerID != winner.ID {
		t.Fatalf("latch re-pointed to %q, want winner %q", d.servedKVSlot.providerID, winner.ID)
	}
}

// TestKVBackendAttributionGenuineFaultLiveWinnerNoRegistryLock covers the
// genuine-fault / live-winner branch: a sticky genuine fault latched from an
// earlier attempt, but a survivor is producing content. Live content names its
// own serving slot (d.provider), resolved without the registry lock, and the
// frozen fault snapshot is not consulted while a live winner remains.
func TestKVBackendAttributionGenuineFaultLiveWinnerNoRegistryLock(t *testing.T) {
	srv, reg, _, ts := setupTestServer(t)
	defer ts.Close()

	const model = "fault-model"
	faulted := seedKVBackend(t, reg, "fault-faulted", model, registry.KVBackendPaged)
	winner := seedKVBackend(t, reg, "fault-winner", model, registry.KVBackendContiguous)

	// A genuine fault from the faulted provider froze the latch/snapshot.
	d := &dispatchState{s: srv, model: model}
	d.genuineFault = &dispatchTerminalFailure{
		attribution: d.providerSlotAttribution(faulted, model),
	}
	// A survivor is now the live winner producing content.
	winnerPR := &registry.PendingRequest{RequestID: "rw", Model: model, ProviderID: winner.ID}
	d.provider = winner
	d.pr = winnerPR

	var got kvBackendAttribution
	runWhileWriteLockHeld(t, reg, func() { got = d.kvBackendAttribution() })

	if got.Backend != registry.KVBackendContiguous {
		t.Fatalf("live-winner attribution backend = %q, want contiguous", got.Backend)
	}
}

// TestFrozenTerminalAttributionSurvivesReLatchNoRegistryLock covers frozen
// terminal attribution: once a genuine fault has latched, a later neutral
// re-latch attempt must NOT move the attribution to a different provider, and
// the exhausted path must return the frozen fault's own slot snapshot. Both run
// under the write lock without touching the registry.
func TestFrozenTerminalAttributionSurvivesReLatchNoRegistryLock(t *testing.T) {
	srv, reg, _, ts := setupTestServer(t)
	defer ts.Close()

	const model = "frozen-model"
	faulted := seedKVBackend(t, reg, "frozen-faulted", model, registry.KVBackendPaged)
	other := seedKVBackend(t, reg, "frozen-other", model, registry.KVBackendContiguous)

	d := &dispatchState{s: srv, model: model}
	faultAttr := d.providerSlotAttribution(faulted, model)
	failure := dispatchTerminalFailure{attribution: faultAttr}
	d.genuineFault = &failure
	d.servedKVSlot = faultAttr // frozen at the fault's slot

	// A neutral re-latch to another provider must be a no-op (freeze rule), and
	// the exhausted attribution must still name the fault's slot. Both under lock.
	otherPR := &registry.PendingRequest{RequestID: "ro", Model: model, ProviderID: other.ID}
	var exhausted kvBackendAttribution
	runWhileWriteLockHeld(t, reg, func() {
		d.noteServingSlotFor(other, otherPR)
		exhausted = d.exhaustedKVBackendAttribution(failure, true)
	})

	if d.servedKVSlot.providerID != faulted.ID {
		t.Fatalf("frozen latch moved to %q, want faulted %q", d.servedKVSlot.providerID, faulted.ID)
	}
	if exhausted.Backend != registry.KVBackendPaged {
		t.Fatalf("frozen exhausted attribution = %q, want paged (the fault's slot)", exhausted.Backend)
	}
}

func TestServingSlotMissingOrMismatchedProviderNoRegistryLock(t *testing.T) {
	srv, reg, _, ts := setupTestServer(t)
	defer ts.Close()
	const model = "pointer-match-model"
	actual := seedKVBackend(t, reg, "pointer-actual", model, registry.KVBackendContiguous)
	wrong := seedKVBackend(t, reg, "pointer-wrong", model, registry.KVBackendPaged)
	pr := &registry.PendingRequest{RequestID: "pointer-request", ProviderID: actual.ID, Model: model}
	for _, tc := range []struct {
		name     string
		provider *registry.Provider
	}{{"nil", nil}, {"mismatched", wrong}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("latch", func(t *testing.T) {
				d := &dispatchState{s: srv, model: model, provider: tc.provider, pr: pr}
				runWhileWriteLockHeld(t, reg, func() { d.noteServingSlotFor(tc.provider, pr) })
				if got := d.servedKVSlot.backend; got != newUnknownKVBackendAttribution() {
					t.Fatalf("invalid provider latch = %+v, want unknown backend and fallback", got)
				}
				if d.servedKVSlot.providerID != "" {
					t.Fatalf("invalid provider attributed to %q", d.servedKVSlot.providerID)
				}
			})
			t.Run("live", func(t *testing.T) {
				d := &dispatchState{s: srv, model: model, provider: tc.provider, pr: pr}
				var got kvBackendAttribution
				runWhileWriteLockHeld(t, reg, func() { got = d.kvBackendAttribution() })
				if got != newUnknownKVBackendAttribution() {
					t.Fatalf("invalid live provider = %+v, want unknown backend and fallback", got)
				}
				if d.servedKVSlot.backend != newUnknownKVBackendAttribution() {
					t.Fatalf("invalid live provider saved another slot's attribution: %+v", d.servedKVSlot)
				}
			})
		})
	}
}
