package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eigeninference/d-inference/coordinator/protocol"
	"nhooyr.io/websocket"
)

// TestServingSlotLatchDoesNotWaitOnRegistryWriteLock is the deterministic
// regression for the post-handoff serving-slot bookkeeping stalling the first
// client byte. TestFirstByteReachesClientWhileRegistryWriteLockHeld exercises
// the same interval but races the write-lock acquisition against the dispatch
// goroutine reaching the latch, so it reproduces the bug only intermittently.
//
// This test forces the exact ordering through the real dispatch/HTTP path with
// the beforeServingSlotAttribution seam: the seam fires on the dispatch
// goroutine AFTER the inference frame is handed off and BEFORE the serving-slot
// KV latch. The test parks the dispatch goroutine there, takes the registry
// WRITE lock, and only then lets the latch run. Before the fix the latch
// resolved the slot through Registry.SlotKVBackendTags -> Registry.GetProvider,
// which takes the registry READ lock, so the dispatch goroutine blocked on the
// held write lock and the first byte never reached the client. After the fix
// the latch reads the provider the goroutine already holds and never touches
// the registry, so the first byte is delivered while the lock is still held.
func TestServingSlotLatchDoesNotWaitOnRegistryWriteLock(t *testing.T) {
	srv, reg, _, ts := setupTestServer(t)
	defer ts.Close()

	// Seam: park the dispatch goroutine between handoff and the serving-slot
	// latch until the test holds the registry write lock, then let it run.
	reachedLatch := make(chan struct{})
	proceed := make(chan struct{})
	var reachedOnce, proceedOnce sync.Once
	// releaseProceed is idempotent and deferred: any early t.Fatal below still
	// unparks the dispatch goroutine from the seam, so httptest's Close never
	// waits on a handler blocked at <-proceed.
	releaseProceed := func() { proceedOnce.Do(func() { close(proceed) }) }
	defer releaseProceed()
	srv.beforeServingSlotAttribution = func() {
		reachedOnce.Do(func() { close(reachedLatch) })
		<-proceed
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pubKey := testPublicKeyB64()
	const model = "serving-slot-lock-model"
	models := []protocol.ModelInfo{{ID: model, ModelType: "chat", Quantization: "4bit"}}
	conn := connectProvider(t, ctx, ts.URL, models, pubKey)
	defer conn.Close(websocket.StatusNormalClosure, "")

	challengeCtx, challengeCancel := context.WithTimeout(ctx, 5*time.Second)
	waitForChallenge(t, challengeCtx, conn, pubKey)
	challengeCancel()
	time.Sleep(200 * time.Millisecond)
	makeProviderRoutable(reg)

	gotRequest := make(chan protocol.InferenceRequestMessage, 1)
	sendChunkNow := make(chan struct{})
	sendCompleteNow := make(chan struct{})
	// Idempotent, deferred releases: an early t.Fatal still lets the provider
	// goroutine and the streaming handler drain to completion (LIFO with
	// releaseProceed above), so no goroutine is left blocked on a gate and
	// httptest's Close does not wait on an outstanding request.
	var chunkOnce, completeOnce sync.Once
	releaseChunk := func() { chunkOnce.Do(func() { close(sendChunkNow) }) }
	releaseComplete := func() { completeOnce.Do(func() { close(sendCompleteNow) }) }
	defer releaseComplete()
	defer releaseChunk()
	providerErr := make(chan error, 1)
	go func() {
		var inferReq protocol.InferenceRequestMessage
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				providerErr <- err
				return
			}
			var env struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(data, &env)
			switch env.Type {
			case protocol.TypeAttestationChallenge:
				_ = conn.Write(ctx, websocket.MessageText, makeValidChallengeResponse(data, pubKey))
				continue
			case protocol.TypeInferenceRequest:
				_ = json.Unmarshal(data, &inferReq)
			default:
				continue
			}
			break
		}
		gotRequest <- inferReq
		<-sendChunkNow
		writeEncryptedTestChunk(t, ctx, conn, inferReq, pubKey,
			`data: {"id":"chatcmpl-1","choices":[{"delta":{"content":"Hello"}}]}`+"\n\n")
		<-sendCompleteNow
		sendComplete(ctx, conn, inferReq.RequestID, protocol.UsageInfo{PromptTokens: 5, CompletionTokens: 1})
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()

	type result struct {
		resp *http.Response
		err  error
	}
	responses := make(chan result, 1)
	go func() {
		body := `{"model":"` + model + `","messages":[{"role":"user","content":"hi"}],"stream":true}`
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-key")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		responses <- result{resp, err}
	}()

	select {
	case <-gotRequest:
	case err := <-providerErr:
		t.Fatalf("provider read: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("provider never received the inference request")
	}

	// The dispatch goroutine handed the frame off and is parked at the seam,
	// immediately before the serving-slot latch.
	select {
	case <-reachedLatch:
	case <-time.After(10 * time.Second):
		t.Fatal("dispatch goroutine never reached the serving-slot latch seam")
	}

	// Make the provider's first content chunk available before the latch runs
	// so nothing but the latch itself can gate the first byte.
	releaseChunk()

	// Hold the registry write lock, THEN let the latch run under it. A
	// registry read lock inside the latch (the pre-fix path) would now block.
	release := reg.HoldWriteLockForTest()
	released := false
	defer func() {
		if !released {
			release()
		}
	}()
	releaseProceed()

	var resp *http.Response
	select {
	case r := <-responses:
		if r.err != nil {
			t.Fatalf("request: %v", r.err)
		}
		resp = r.resp
	case <-time.After(2 * time.Second):
		t.Fatal("response headers did not reach the client within 2 s while the registry write lock was held (serving-slot latch took the registry read lock)")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	firstBytes := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		firstBytes <- string(buf[:n])
	}()
	select {
	case first := <-firstBytes:
		if !strings.Contains(first, "Hello") {
			t.Fatalf("first bytes = %q, want the provider's content", first)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first chunk did not reach the client within 2 s while the registry write lock was held")
	}

	release()
	released = true
	releaseComplete()

	deadline := time.Now().Add(5 * time.Second)
	buf := make([]byte, 4096)
	var rest strings.Builder
	for time.Now().Before(deadline) {
		n, err := resp.Body.Read(buf)
		rest.Write(buf[:n])
		if err != nil {
			break
		}
		if strings.Contains(rest.String(), "[DONE]") {
			break
		}
	}
	if !strings.Contains(rest.String(), "[DONE]") {
		t.Fatalf("stream did not finish after the lock was released: %q", rest.String())
	}
}
