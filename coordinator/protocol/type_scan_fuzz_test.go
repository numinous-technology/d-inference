package protocol

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// referenceDecodeProviderMessage is a deliberately independent reimplementation
// of ProviderMessage.UnmarshalJSON's dispatch. It never touches the fast
// scanner (scanTopLevelString / scanChunkFrame): it reads "type" with a plain
// encoding/json envelope pass, then unmarshals the concrete struct for that
// type exactly as the production switch does. FuzzProviderMessageScanEquivalence
// holds the production scanner path to this reference, so a dispatch bug (a
// case wired to the wrong struct, a dropped case, a scanner that reads the
// wrong "type") shows up as a decoded-value or accept/reject divergence rather
// than passing unnoticed through both paths.
//
// The property under test: for any input bytes, DecodeProviderMessage accepts
// exactly when this reference accepts, and when both accept they produce equal
// ProviderMessage values. Error *wording* is intentionally not compared — only
// whether an error occurred.
func referenceDecodeProviderMessage(data []byte, pm *ProviderMessage) error {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("reference: failed to read message type: %w", err)
	}
	pm.Type = envelope.Type

	// decode unmarshals into a fresh value of T, mirroring one switch arm.
	decode := func(target any) error {
		if err := json.Unmarshal(data, target); err != nil {
			return fmt.Errorf("reference: failed to unmarshal %s: %w", envelope.Type, err)
		}
		pm.Payload = target
		return nil
	}

	switch envelope.Type {
	case TypeRegister:
		return decode(&RegisterMessage{})
	case TypeHeartbeat:
		return decode(&HeartbeatMessage{})
	case TypeInferenceAccepted:
		return decode(&InferenceAcceptedMessage{})
	case TypeInferenceResponseChunk:
		return decode(&InferenceResponseChunkMessage{})
	case TypeInferenceComplete:
		return decode(&InferenceCompleteMessage{})
	case TypeInferenceError:
		return decode(&InferenceErrorMessage{})
	case TypeAttestationResponse:
		return decode(&AttestationResponseMessage{})
	case TypeCodeAttestationResponse:
		return decode(&CodeAttestationResponseMessage{})
	case TypeLoadModelStatus:
		return decode(&LoadModelStatusMessage{})
	case TypePrefetchModelStatus:
		return decode(&PrefetchModelStatusMessage{})
	case TypeModelsUpdate:
		return decode(&ModelsUpdateMessage{})
	case TypePrefixCacheLookup:
		return decode(&PrefixCacheLookupMessage{})
	case TypePrefixCacheReady:
		return decode(&PrefixCacheReadyMessage{})
	case TypePrefixCacheLookupV2:
		return decode(&PrefixCacheLookupV2Message{})
	case TypePrefixCacheReadyV2:
		return decode(&PrefixCacheReadyV2Message{})
	case TypeCapacityQuote:
		return decode(&CapacityQuoteMessage{})
	default:
		return fmt.Errorf("reference: unknown message type %q", envelope.Type)
	}
}

// assertProviderMessageScanEquivalent holds the production scanner dispatch
// (DecodeProviderMessage) to the independent envelope-based reference: they
// must agree on whether the frame is accepted, and on the decoded value when
// both accept.
func assertProviderMessageScanEquivalent(t testing.TB, data []byte) {
	t.Helper()
	var direct, reference ProviderMessage
	directErr := DecodeProviderMessage(data, &direct)
	referenceErr := referenceDecodeProviderMessage(data, &reference)

	if (directErr == nil) != (referenceErr == nil) {
		t.Fatalf("acceptance mismatch for %q:\n  DecodeProviderMessage err = %v\n  reference err = %v",
			data, directErr, referenceErr)
	}
	if directErr != nil {
		return // both rejected; wording is not compared
	}
	if direct.Type != reference.Type {
		t.Fatalf("type mismatch for %q: direct=%q reference=%q", data, direct.Type, reference.Type)
	}
	if !reflect.DeepEqual(direct, reference) {
		t.Fatalf("decoded value mismatch for %q:\n  direct    %+v\n  reference %+v", data, direct, reference)
	}
}

// providerMessageScanSeeds are representative provider frames plus adversarial
// shapes that exercise the dispatch path: escaped and case-variant type keys,
// duplicate keys, nested data before the type key, malformed JSON, and unknown
// types. Every one must decode identically through the production scanner path
// and the independent envelope reference (or be rejected by both).
func providerMessageScanSeeds() [][]byte {
	seeds := [][]byte{
		// Representative message types (well-formed).
		[]byte(`{"type":"register","hardware":{"machine_model":"Mac15,8","chip_name":"Apple M3 Max","memory_gb":64},"models":[{"id":"m","size_bytes":1}],"backend":"vllm_mlx","public_key":"cGs="}`),
		[]byte(`{"type":"heartbeat","status":"idle","active_model":"m","warm_models":["m"],"stats":{"requests_served":3}}`),
		[]byte(`{"type":"inference_accepted","request_id":"r"}`),
		[]byte(`{"type":"inference_response_chunk","request_id":"r","data":"data: [DONE]"}`),
		[]byte(`{"type":"inference_response_chunk","request_id":"r","encrypted_data":{"ephemeral_public_key":"a","ciphertext":"b"}}`),
		[]byte(`{"type":"inference_complete","request_id":"r","usage":{"prompt_tokens":1,"completion_tokens":2}}`),
		[]byte(`{"type":"inference_error","request_id":"r","error":"boom","status_code":503,"error_reason":"model_not_loaded"}`),
		[]byte(`{"type":"attestation_response","request_id":"r"}`),
		[]byte(`{"type":"code_attestation_response","request_id":"r"}`),
		[]byte(`{"type":"load_model_status","model":"m","status":"loaded"}`),
		[]byte(`{"type":"prefetch_model_status","model":"m","status":"done"}`),
		[]byte(`{"type":"models_update","models":[{"id":"m","size_bytes":1}]}`),
		[]byte(`{"type":"prefix_cache_lookup","request_id":"r"}`),
		[]byte(`{"type":"prefix_cache_ready","request_id":"r"}`),
		[]byte(`{"type":"prefix_cache_lookup_v2","request_id":"r"}`),
		[]byte(`{"type":"prefix_cache_ready_v2","request_id":"r"}`),
		[]byte(`{"type":"capacity_quote","request_id":"r"}`),

		// Nested data before the type key (whole objects/arrays skipped).
		[]byte(`{"stats":{"type":"decoy","n":1},"models":[{"type":"inner"},["type",2]],"type":"register","backend":"b","public_key":"k"}`),
		[]byte(`{"request_id":"r","usage":{"prompt_tokens":1},"profile":{"schema":1,"engine":{"type":"decoy"}},"type":"inference_complete"}`),

		// Escaped type value and escaped keys: scanner defers to envelope decode.
		[]byte(`{"type":"heart\u0062eat","status":"idle"}`),
		[]byte(`{"no\u0074e":1,"type":"heartbeat"}`),
		// Directly escaped `type` key: the scanner's raw byte compare never sees
		// "type", so it must fall back to the envelope decode that unescapes the
		// key. The reference always unescapes, so both must land on heartbeat.
		[]byte(`{"ty\u0070e":"heartbeat","status":"idle"}`),

		// Case-variant type keys (encoding/json is case-insensitive).
		[]byte(`{"Type":"heartbeat","status":"idle"}`),
		[]byte(`{"TYPE":"register","backend":"b","public_key":"k"}`),

		// Duplicate keys (last-match-wins in encoding/json).
		[]byte(`{"type":"heartbeat","type":"register","backend":"b","public_key":"k"}`),
		[]byte(`{"type":"register","status":"a","status":"b","backend":"b","public_key":"k"}`),
		// Mixed-case duplicate `type` across two *different* known types: an
		// exact-case "type" (heartbeat) followed by a case-variant "TYPE"
		// (register). encoding/json matches both case-insensitively and takes
		// last-match-wins, so the frame must dispatch to register, not heartbeat.
		// The scanner sees the case variant, bails on the duplicate, and defers
		// to the envelope decode that agrees.
		[]byte(`{"type":"heartbeat","status":"idle","TYPE":"register","backend":"b","public_key":"k"}`),

		// Non-string / null / missing type.
		[]byte(`{"type":123}`),
		[]byte(`{"type":null}`),
		[]byte(`{"status":"idle"}`),

		// Unknown types.
		[]byte(`{"type":"totally_unknown"}`),
		[]byte(`{"type":""}`),
		[]byte(`{"type":"heartbeat_v2","status":"idle"}`),

		// Type-mismatched fields under a known type (concrete decode must fail).
		[]byte(`{"type":"heartbeat","status":123}`),
		[]byte(`{"type":"inference_complete","usage":"nope"}`),

		// Malformed JSON, truncation, trailing garbage, wrong top-level kind.
		[]byte(`{"type":"heartbeat"`),
		[]byte(`{"type":"heartbeat"} trailing`),
		[]byte(`{"type":"totally_unknown"} trailing`),
		[]byte(`{"type":"heartbeat",}`),
		[]byte(`[{"type":"heartbeat"}]`),
		[]byte(`"heartbeat"`),
		[]byte(`null`),
		[]byte(``),
		[]byte(`{}`),
	}
	// Chunk-frame corpus doubles as provider-message input for this path.
	for _, tc := range chunkFrameCorpus {
		seeds = append(seeds, []byte(tc.frame))
	}
	return seeds
}

// TestProviderMessageScanEquivalenceSeeds runs the differential check over the
// fixed seed corpus so the property is exercised in a plain `go test` run, not
// only under `go test -fuzz`.
func TestProviderMessageScanEquivalenceSeeds(t *testing.T) {
	for i, seed := range providerMessageScanSeeds() {
		t.Run(fmt.Sprintf("seed-%02d", i), func(t *testing.T) {
			assertProviderMessageScanEquivalent(t, seed)
		})
	}
}

// FuzzProviderMessageScanEquivalence is the differential fuzz target for the
// provider-message dispatch path. It compares the production scanner-based
// decode (DecodeProviderMessage -> ProviderMessage.UnmarshalJSON, using
// scanTopLevelString / scanChunkFrame) against referenceDecodeProviderMessage,
// which reads "type" with a standard JSON envelope decode and then unmarshals
// the matching concrete struct. The two must agree on acceptance and, when
// both accept, on the decoded ProviderMessage value. The fast path must never
// panic, and it must never decode a frame the reference dispatch rejects (or
// vice versa). Error wording is not required to match.
func FuzzProviderMessageScanEquivalence(f *testing.F) {
	for _, seed := range providerMessageScanSeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 8192 {
			t.Skip()
		}
		assertProviderMessageScanEquivalent(t, data)
	})
}
