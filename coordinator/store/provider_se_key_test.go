package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestProviderBySEKeyContract pins the reconnect-lookup contract on BOTH the
// memory store and a real isolated PostgreSQL (via DATABASE_URL, using
// storeBackends). GetProviderBySEKey returns the freshest DEVICE METADATA row
// for a Secure Enclave key; GetReputationBySEKey returns the standing from the
// newest reputation-bearing row for that key. Both exclude the registering
// session and never inherit across a different SE key.
func TestProviderBySEKeyContract(t *testing.T) {
	for name, st := range storeBackends(t) {
		st := st
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Millisecond)

			seedProvider := func(id, seKey, account string, seen time.Time) {
				if err := st.UpsertProvider(ctx, ProviderRecord{
					ID: id, SEPublicKey: seKey, AccountID: account, Hardware: json.RawMessage(`{}`), Models: json.RawMessage(`[]`),
					TrustLevel: "hardware", Attested: true, LastSeen: seen,
				}); err != nil {
					t.Fatalf("UpsertProvider %s: %v", id, err)
				}
			}
			seedRep := func(id string, jobs int) {
				if err := st.UpsertReputation(ctx, id, ReputationRecord{
					TotalJobs: jobs, SuccessfulJobs: jobs,
				}); err != nil {
					t.Fatalf("UpsertReputation %s: %v", id, err)
				}
			}

			const seKey = "se-key-alpha"

			// Newer session carries fresh device metadata but NO reputation row;
			// an older session carries the earned standing.
			seedProvider("old-earned", seKey, "old-account", now.Add(-2*time.Hour))
			seedRep("old-earned", 40)
			seedProvider("new-bare", seKey, "new-account", now.Add(-1*time.Hour))

			// A different SE key must never be inherited.
			seedProvider("stranger", "se-key-beta", "beta-account", now)
			seedRep("stranger", 999)

			// Metadata: newest row wins regardless of reputation presence.
			rec, err := st.GetProviderBySEKey(ctx, seKey, "registering")
			if err != nil {
				t.Fatalf("GetProviderBySEKey: %v", err)
			}
			if rec == nil || rec.ID != "new-bare" || rec.AccountID != "new-account" {
				t.Fatalf("metadata lookup should pick newest row (new-bare/new-account), got %+v", rec)
			}

			// Reputation: earned standing from the older row survives even though
			// the newest row has none.
			rep, err := st.GetReputationBySEKey(ctx, seKey, "registering")
			if err != nil {
				t.Fatalf("GetReputationBySEKey: %v", err)
			}
			if rep == nil || rep.TotalJobs != 40 {
				t.Fatalf("reputation lookup should recover 40 from old-earned, got %+v", rep)
			}

			// Different SE key is isolated.
			betaRep, err := st.GetReputationBySEKey(ctx, "se-key-beta", "registering")
			if err != nil {
				t.Fatalf("GetReputationBySEKey beta: %v", err)
			}
			if betaRep == nil || betaRep.TotalJobs != 999 {
				t.Fatalf("beta key must see its own 999, got %+v", betaRep)
			}
			if betaMeta, _ := st.GetProviderBySEKey(ctx, "se-key-beta", "registering"); betaMeta == nil || betaMeta.AccountID != "beta-account" {
				t.Fatalf("beta key metadata isolation failed: %+v", betaMeta)
			}

			// Empty SE key never matches.
			if r, err := st.GetProviderBySEKey(ctx, "", "registering"); err != nil || r != nil {
				t.Fatalf("empty SE key must return (nil,nil), got (%+v,%v)", r, err)
			}
			if r, err := st.GetReputationBySEKey(ctx, "", "registering"); err != nil || r != nil {
				t.Fatalf("empty SE key reputation must return (nil,nil), got (%+v,%v)", r, err)
			}
			// Unknown SE key never matches.
			if r, err := st.GetProviderBySEKey(ctx, "se-key-missing", "registering"); err != nil || r != nil {
				t.Fatalf("unknown SE key must return (nil,nil), got (%+v,%v)", r, err)
			}
			if r, err := st.GetReputationBySEKey(ctx, "se-key-missing", "registering"); err != nil || r != nil {
				t.Fatalf("unknown SE key reputation must return (nil,nil), got (%+v,%v)", r, err)
			}
		})
	}
}

// TestProviderBySEKeyExcludesRegisteringSession covers the core reset bug: the
// registering session persists its own bare/empty row before restoration runs,
// and that row must be excluded from both lookups so it cannot shadow prior
// standing under the same SE key.
func TestProviderBySEKeyExcludesRegisteringSession(t *testing.T) {
	for name, st := range storeBackends(t) {
		st := st
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC()
			const seKey = "se-key-exclude"

			// Prior session earned standing.
			if err := st.UpsertProvider(ctx, ProviderRecord{
				ID: "prior", SEPublicKey: seKey, Hardware: json.RawMessage(`{}`), Models: json.RawMessage(`[]`), TrustLevel: "hardware",
				Attested: true, LastSeen: now.Add(-time.Hour),
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.UpsertReputation(ctx, "prior", ReputationRecord{TotalJobs: 75, SuccessfulJobs: 75}); err != nil {
				t.Fatal(err)
			}
			// The registering session's own row is the newest and carries an absurd
			// decoy counter; excluding it must make prior win.
			if err := st.UpsertProvider(ctx, ProviderRecord{
				ID: "registering", SEPublicKey: seKey, Hardware: json.RawMessage(`{}`), Models: json.RawMessage(`[]`), TrustLevel: "hardware",
				Attested: true, LastSeen: now,
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.UpsertReputation(ctx, "registering", ReputationRecord{TotalJobs: 999, SuccessfulJobs: 999}); err != nil {
				t.Fatal(err)
			}

			rec, err := st.GetProviderBySEKey(ctx, seKey, "registering")
			if err != nil {
				t.Fatalf("GetProviderBySEKey: %v", err)
			}
			if rec == nil || rec.ID != "prior" {
				t.Fatalf("registering session must be excluded, got %+v", rec)
			}
			rep, err := st.GetReputationBySEKey(ctx, seKey, "registering")
			if err != nil {
				t.Fatalf("GetReputationBySEKey: %v", err)
			}
			if rep == nil || rep.TotalJobs != 75 {
				t.Fatalf("registering decoy reputation must be excluded, got %+v", rep)
			}

			// With no other row, excluding the only (registering) row yields none.
			if r, _ := st.GetProviderBySEKey(ctx, "se-key-solo", "solo"); r != nil {
				t.Fatalf("no match expected for solo key")
			}
		})
	}
}

// TestProviderBySEKeyEqualTimestampsDeterministic pins the shared tie-break:
// with equal LastSeen the greater id wins, identically on memory and Postgres,
// so 1,000 repeated queries never flip between candidates.
func TestProviderBySEKeyEqualTimestampsDeterministic(t *testing.T) {
	for name, st := range storeBackends(t) {
		st := st
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			ts := time.Now().UTC().Truncate(time.Millisecond)
			const seKey = "se-key-tie"

			for _, id := range []string{"aaa", "bbb", "ccc"} {
				if err := st.UpsertProvider(ctx, ProviderRecord{
					ID: id, SEPublicKey: seKey, AccountID: "acct-" + id, Hardware: json.RawMessage(`{}`), Models: json.RawMessage(`[]`),
					TrustLevel: "hardware", Attested: true, LastSeen: ts,
				}); err != nil {
					t.Fatal(err)
				}
				if err := st.UpsertReputation(ctx, id, ReputationRecord{TotalJobs: 1}); err != nil {
					t.Fatal(err)
				}
			}

			for i := 0; i < 1000; i++ {
				rec, err := st.GetProviderBySEKey(ctx, seKey, "registering")
				if err != nil {
					t.Fatalf("GetProviderBySEKey: %v", err)
				}
				if rec == nil || rec.ID != "ccc" {
					t.Fatalf("iteration %d: equal-timestamp tie-break must pick greatest id 'ccc', got %+v", i, rec)
				}
			}
		})
	}
}

// TestGetProviderBySEKeyReturnsDeepCopy proves the memory getter does not alias
// stored mutable fields: mutating the returned Hardware / Location must not
// corrupt the stored record. Scoped to the new API.
func TestGetProviderBySEKeyReturnsDeepCopy(t *testing.T) {
	st := NewMemory(Config{})
	ctx := context.Background()
	const seKey = "se-key-copy"
	hardware := json.RawMessage(`{"model":"Mac17,6"}`)
	if err := st.UpsertProvider(ctx, ProviderRecord{
		ID: "sess", SEPublicKey: seKey, Hardware: hardware, Models: json.RawMessage(`[]`),
		Location:   &ProviderLocation{City: "Original"},
		TrustLevel: "hardware", Attested: true, LastSeen: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	rec, err := st.GetProviderBySEKey(ctx, seKey, "registering")
	if err != nil || rec == nil {
		t.Fatalf("GetProviderBySEKey: rec=%v err=%v", rec, err)
	}
	// Mutate every returned mutable field.
	for i := range rec.Hardware {
		rec.Hardware[i] = 'X'
	}
	rec.Location.City = "Mutated"

	again, err := st.GetProviderBySEKey(ctx, seKey, "registering")
	if err != nil || again == nil {
		t.Fatalf("GetProviderBySEKey re-read: rec=%v err=%v", again, err)
	}
	if string(again.Hardware) != `{"model":"Mac17,6"}` {
		t.Fatalf("stored Hardware was aliased and mutated: %s", again.Hardware)
	}
	if again.Location == nil || again.Location.City != "Original" {
		t.Fatalf("stored Location was aliased and mutated: %+v", again.Location)
	}
}
