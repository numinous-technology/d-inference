package protocol

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzInferenceErrorWireIsolation exercises JSON roundtrips for
// InferenceErrorMessage across varying request IDs, human messages, valid
// failure codes, and adversarial attempts to inject the coordinator-only
// CoordinatorCause field.
//
// It pins two invariants that inference_failure_test.go asserts for fixed
// inputs, and that must hold for arbitrary input:
//
//  1. Egress isolation: the internal CoordinatorCause (json:"-") never appears
//     in serialized wire JSON, regardless of what value it holds.
//  2. Ingress isolation: no incoming JSON — however the key is cased, escaped,
//     or duplicated — can set CoordinatorCause on a decoded message.
//
// It also asserts that documented public fields survive valid roundtrips so
// the isolation guarantee is not achieved by silently dropping legitimate data.
func FuzzInferenceErrorWireIsolation(f *testing.F) {
	// Seed with the valid failure-code vocabulary paired with realistic and
	// tricky request IDs / messages.
	validCodes := []string{
		string(FailureCodeInvalidRequest),
		string(FailureCodeInvalidMedia),
		string(FailureCodeMediaTooLarge),
		string(FailureCodeUnsupportedMedia),
		string(FailureCodeTemplateRender),
		string(FailureCodeModelUnavailable),
		string(FailureCodeCapacity),
		string(FailureCodeCancelled),
		string(FailureCodeEncryptionFailure),
		string(FailureCodeGenerationFailure),
		string(FailureCodeInternalFailure),
	}

	requestIDs := []string{
		"req-1",
		"",
		"req with spaces",
		`req"quote`,
		"req\nnewline",
		"req\\backslash",
		"reqémoji🔒",
		`{"nested":"json"}`,
	}
	messages := []string{
		"inference generation failed",
		"",
		`error with "quotes" and \backslash`,
		"provider_disconnected",
		"coordinator",
		"CoordinatorCause",
		"line1\nline2\ttab",
		`{"coordinator_cause":"provider_disconnected"}`,
	}
	// Adversarial coordinator_cause injection payloads: the provider trying to
	// smuggle a control-plane-only cause through any wire-decodable channel.
	causeInjections := []string{
		"",
		"provider_disconnected",
		"provider_restart",
		"coordinator",
		"arbitrary",
	}

	for i, code := range validCodes {
		f.Add(
			requestIDs[i%len(requestIDs)],
			messages[i%len(messages)],
			code,
			500,
			causeInjections[i%len(causeInjections)],
		)
	}
	// A couple of pure edge seeds.
	f.Add("", "", "", 0, "provider_disconnected")
	f.Add(`req"\n`, `msg"\t`, "generation_failure", 502, "coordinator")
	f.Add("CoordinatorCause", `{"CoordinatorCause":"x"}`, "internal_failure", 500, "provider_restart")

	f.Fuzz(func(t *testing.T, requestID, message, failureCode string, statusCode int, causeInjection string) {
		// --- Egress: marshaling never leaks CoordinatorCause onto the wire. ---
		msg := InferenceErrorMessage{
			Type:             TypeInferenceError,
			RequestID:        requestID,
			Error:            message,
			StatusCode:       statusCode,
			FailureCode:      InferenceFailureCode(failureCode),
			CoordinatorCause: CoordinatorInferenceErrorCause(causeInjection),
		}
		b, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal InferenceErrorMessage: %v", err)
		}

		// The struct has no wire key for CoordinatorCause (json:"-"), so its
		// JSON representation must contain no such key regardless of casing.
		var asMap map[string]json.RawMessage
		if err := json.Unmarshal(b, &asMap); err != nil {
			t.Fatalf("re-unmarshal marshaled message into map: %v (json=%s)", err, b)
		}
		for k := range asMap {
			switch strings.ToLower(k) {
			case "coordinatorcause", "coordinator_cause":
				t.Fatalf("coordinator-only cause key %q leaked onto wire: %s", k, b)
			}
		}

		// Byte-exactness invariant: whatever value CoordinatorCause holds must
		// contribute NOTHING to the wire. Marshaling the same message with the
		// cause cleared must be byte-identical. This is stronger than a substring
		// scan (which would false-positive when a short cause string collides
		// with structural JSON like the "failure_code" key) and directly proves
		// the value never rides the wire.
		cleared := msg
		cleared.CoordinatorCause = ""
		clearedBytes, err := json.Marshal(cleared)
		if err != nil {
			t.Fatalf("marshal cleared message: %v", err)
		}
		if string(b) != string(clearedBytes) {
			t.Fatalf("CoordinatorCause=%q changed wire bytes: with=%s without=%s", causeInjection, b, clearedBytes)
		}

		// --- Roundtrip: public fields survive marshal → unmarshal. ---
		var round InferenceErrorMessage
		if err := json.Unmarshal(b, &round); err != nil {
			t.Fatalf("roundtrip unmarshal: %v (json=%s)", err, b)
		}
		if round.StatusCode != statusCode {
			t.Fatalf("status_code roundtrip: got %d want %d", round.StatusCode, statusCode)
		}
		if round.Type != TypeInferenceError {
			t.Fatalf("type roundtrip: got %q want %q", round.Type, TypeInferenceError)
		}
		// Exact string equality only holds for valid UTF-8 input: encoding/json
		// deliberately replaces invalid UTF-8 bytes with U+FFFD on marshal
		// (documented behavior, not a wire-compat bug), so skip the byte-exact
		// comparison for non-UTF-8 inputs while still exercising isolation above.
		if utf8.ValidString(requestID) && round.RequestID != requestID {
			t.Fatalf("request_id roundtrip: got %q want %q", round.RequestID, requestID)
		}
		if utf8.ValidString(message) && round.Error != message {
			t.Fatalf("error roundtrip: got %q want %q", round.Error, message)
		}
		if utf8.ValidString(failureCode) && round.FailureCode != InferenceFailureCode(failureCode) {
			t.Fatalf("failure_code roundtrip: got %q want %q", round.FailureCode, failureCode)
		}
		// CoordinatorCause must never survive a wire roundtrip.
		if round.CoordinatorCause != "" {
			t.Fatalf("coordinator cause survived wire roundtrip: %q", round.CoordinatorCause)
		}

		// A valid failure code must still validate after the roundtrip; an
		// off-vocabulary one must not (semantics preserved from
		// inference_failure_test.go). All vocabulary codes are valid UTF-8, so
		// this only meaningfully differs when the input was already invalid.
		if utf8.ValidString(failureCode) &&
			round.FailureCode.Valid() != InferenceFailureCode(failureCode).Valid() {
			t.Fatalf("failure-code validity changed across roundtrip for %q", failureCode)
		}

		// --- Ingress: adversarial incoming JSON cannot set CoordinatorCause. ---
		// Build hostile documents that try every plausible key spelling. Use a
		// valid code so decode succeeds and we isolate the cause injection.
		reqB, _ := json.Marshal(requestID)
		msgB, _ := json.Marshal(message)
		causeB, _ := json.Marshal(causeInjection)
		hostiles := []string{
			`{"type":"inference_error","request_id":` + string(reqB) + `,"error":` + string(msgB) + `,"status_code":500,"failure_code":"internal_failure","CoordinatorCause":` + string(causeB) + `}`,
			`{"type":"inference_error","request_id":` + string(reqB) + `,"error":` + string(msgB) + `,"status_code":500,"failure_code":"internal_failure","coordinator_cause":` + string(causeB) + `}`,
			`{"type":"inference_error","request_id":` + string(reqB) + `,"error":` + string(msgB) + `,"status_code":500,"failure_code":"internal_failure","coordinatorCause":` + string(causeB) + `}`,
			// Duplicate keys: a public field followed by an injection attempt.
			`{"type":"inference_error","request_id":` + string(reqB) + `,"error":` + string(msgB) + `,"status_code":500,"failure_code":"internal_failure","CoordinatorCause":` + string(causeB) + `,"CoordinatorCause":"provider_disconnected"}`,
		}
		for _, doc := range hostiles {
			var in InferenceErrorMessage
			if err := json.Unmarshal([]byte(doc), &in); err != nil {
				// Malformed adversarial input is fine to reject; skip it.
				continue
			}
			if in.CoordinatorCause != "" {
				t.Fatalf("incoming JSON set coordinator-only cause %q via doc: %s", in.CoordinatorCause, doc)
			}
			if in.FailureCode != FailureCodeInternalFailure {
				t.Fatalf("public failure_code not decoded from hostile doc: got %q doc=%s", in.FailureCode, doc)
			}
		}
	})
}
