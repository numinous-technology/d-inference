package protocol

import "testing"

// TestProviderMessageTypeNullAndDuplicateFields pins the decoded semantics of
// duplicate and null "type" fields. A duplicate (or case-variant) "type" key
// forces scanTopLevelString off the fast path, so encoding/json's rules decide
// the outcome: keys are processed in order, last-match-wins, matched
// case-insensitively, and a JSON `null` is a no-op that leaves any prior string
// in place rather than erasing it. A wrong JSON type (number) still fails the
// whole decode even when a later duplicate carries a valid string, because
// encoding/json records the type error and returns it. Expected outcomes are
// written out explicitly so each case exercises the production decoder against
// a fixed contract rather than treating the decoder as its own oracle.
func TestProviderMessageTypeNullAndDuplicateFields(t *testing.T) {
	tests := []struct {
		name string
		in   string
		// accept is true when UnmarshalJSON must succeed; wantType and
		// wantStatus are then checked against the decoded heartbeat payload.
		accept     bool
		wantType   string
		wantStatus string
	}{
		{
			name:       "known type then null",
			in:         `{"type":"heartbeat","status":"idle","type":null}`,
			accept:     true,
			wantType:   TypeHeartbeat,
			wantStatus: "idle",
		},
		{
			name:       "null then known type",
			in:         `{"type":null,"status":"busy","type":"heartbeat"}`,
			accept:     true,
			wantType:   TypeHeartbeat,
			wantStatus: "busy",
		},
		{
			name:       "mixed-case null duplicate",
			in:         `{"type":"heartbeat","status":"draining","Type":null}`,
			accept:     true,
			wantType:   TypeHeartbeat,
			wantStatus: "draining",
		},
		{
			name:   "unknown type then null",
			in:     `{"type":"bogus","type":null}`,
			accept: false,
		},
		{
			name:   "numeric duplicate then known type",
			in:     `{"type":123,"type":"heartbeat"}`,
			accept: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var pm ProviderMessage
			err := pm.UnmarshalJSON([]byte(tc.in))
			if tc.accept {
				if err != nil {
					t.Fatalf("UnmarshalJSON(%s) failed: %v", tc.in, err)
				}
				if pm.Type != tc.wantType {
					t.Fatalf("pm.Type = %q, want %q", pm.Type, tc.wantType)
				}
				hb, ok := pm.Payload.(*HeartbeatMessage)
				if !ok {
					t.Fatalf("payload type = %T, want *HeartbeatMessage", pm.Payload)
				}
				if hb.Type != tc.wantType {
					t.Fatalf("heartbeat.Type = %q, want %q", hb.Type, tc.wantType)
				}
				if hb.Status != tc.wantStatus {
					t.Fatalf("heartbeat.Status = %q, want %q", hb.Status, tc.wantStatus)
				}
				return
			}
			if err == nil {
				t.Fatalf("UnmarshalJSON(%s) accepted, want rejection (payload %T)", tc.in, pm.Payload)
			}
		})
	}
}
