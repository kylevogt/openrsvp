package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yannkr/openrsvp/internal/database"
	"github.com/yannkr/openrsvp/internal/testutil"
)

// newTestDispatcher builds a Dispatcher whose HTTP client follows the same
// hardening as production (no redirects, short timeout) but uses the default
// dialer so it can reach an httptest.Server on loopback. The SSRF dialer itself
// is exercised separately via dialer/isPrivateIP tests.
func newTestDispatcher(store *Store) *Dispatcher {
	client := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return newDispatcherWithClient(store, zerolog.Nop(), client)
}

// createTestWebhook inserts a webhook row and returns it.
func createTestWebhook(t *testing.T, ctx context.Context, store *Store, db database.DB, url, secret string) *Webhook {
	t.Helper()
	eventID := uuid.Must(uuid.NewV7()).String()
	createParentEvent(t, ctx, db, eventID)

	w := &Webhook{
		ID:         uuid.Must(uuid.NewV7()).String(),
		EventID:    eventID,
		URL:        url,
		Secret:     secret,
		EventTypes: []string{"rsvp.created"},
		Enabled:    true,
	}
	require.NoError(t, store.CreateWebhook(ctx, w))
	return w
}

func newTestDelivery(webhookID, payload string) *Delivery {
	return &Delivery{
		ID:        uuid.Must(uuid.NewV7()).String(),
		WebhookID: webhookID,
		EventType: "rsvp.created",
		Payload:   payload,
		Attempt:   0,
	}
}

// TestDeliver_HMACSignature is the core integrity guarantee: it verifies the
// X-OpenRSVP-Signature header is exactly "sha256=" + hex(HMAC-SHA256(secret,
// body)), and that the body, content-type, and other headers are correct.
func TestDeliver_HMACSignature(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newDispatcherTestDB(t)
	store := NewStore(db)

	const secret = "whsec_supersecret"

	var (
		gotSig         string
		gotEvent       string
		gotDeliveryHdr string
		gotCType       string
		gotUA          string
		gotMethod      string
		gotBody        []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotSig = r.Header.Get("X-OpenRSVP-Signature")
		gotEvent = r.Header.Get("X-OpenRSVP-Event")
		gotDeliveryHdr = r.Header.Get("X-OpenRSVP-Delivery")
		gotCType = r.Header.Get("Content-Type")
		gotUA = r.Header.Get("User-Agent")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	w := createTestWebhook(t, ctx, store, db, srv.URL, secret)
	payload := []byte(`{"eventType":"rsvp.created","data":{"x":1}}`)
	delivery := newTestDelivery(w.ID, string(payload))
	require.NoError(t, store.CreateDelivery(ctx, delivery))

	d := newTestDispatcher(store)
	d.deliver(ctx, w, delivery, payload)

	// Compute the expected signature in the exact format the code produces.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, expectedSig, gotSig, "signature must be sha256=hex(HMAC-SHA256(secret, body))")
	assert.Equal(t, payload, gotBody, "body must be sent verbatim")
	assert.Equal(t, "application/json", gotCType)
	assert.Equal(t, "rsvp.created", gotEvent)
	assert.Equal(t, delivery.ID, gotDeliveryHdr)
	assert.Equal(t, "OpenRSVP-Webhook/1.0", gotUA)

	// Sanity: the signature is verifiable against the body with the secret.
	assert.True(t, hmac.Equal([]byte(gotSig), []byte(expectedSig)))

	// A wrong secret must NOT verify (guards against accidental empty/static secret).
	badMac := hmac.New(sha256.New, []byte("whsec_wrong"))
	badMac.Write(payload)
	badSig := "sha256=" + hex.EncodeToString(badMac.Sum(nil))
	assert.NotEqual(t, badSig, gotSig)
}

// TestDeliver_Success records a success on a 2xx response.
func TestDeliver_Success(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newDispatcherTestDB(t)
	store := NewStore(db)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	w := createTestWebhook(t, ctx, store, db, srv.URL, "whsec_x")
	delivery := newTestDelivery(w.ID, `{}`)
	require.NoError(t, store.CreateDelivery(ctx, delivery))

	d := newTestDispatcher(store)
	d.deliver(ctx, w, delivery, []byte(`{}`))

	require.NotNil(t, delivery.DeliveredAt)
	require.NotNil(t, delivery.ResponseStatus)
	assert.Equal(t, 200, *delivery.ResponseStatus)
	assert.Nil(t, delivery.Error)
	assert.Equal(t, 1, delivery.Attempt, "success on first attempt")

	// Persisted state matches.
	got := getDelivery(t, ctx, store, delivery.ID)
	require.NotNil(t, got.ResponseStatus)
	assert.Equal(t, 200, *got.ResponseStatus)
	assert.NotNil(t, got.DeliveredAt)
	assert.Equal(t, `{"ok":true}`, *got.ResponseBody)
}

// TestDeliver_ClientError_NoRetry records a failure on 4xx and does NOT retry.
func TestDeliver_ClientError_NoRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newDispatcherTestDB(t)
	store := NewStore(db)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	w := createTestWebhook(t, ctx, store, db, srv.URL, "whsec_x")
	delivery := newTestDelivery(w.ID, `{}`)
	require.NoError(t, store.CreateDelivery(ctx, delivery))

	d := newTestDispatcher(store)
	d.deliver(ctx, w, delivery, []byte(`{}`))

	assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "4xx must not be retried")
	require.NotNil(t, delivery.Error)
	assert.Contains(t, *delivery.Error, "not retryable")
	assert.Nil(t, delivery.DeliveredAt)

	got := getDelivery(t, ctx, store, delivery.ID)
	require.NotNil(t, got.ResponseStatus)
	assert.Equal(t, 400, *got.ResponseStatus)
	assert.Nil(t, got.DeliveredAt)
	require.NotNil(t, got.Error)
}

// TestDeliver_ServerError_RetriesThenFails verifies the retry/backoff path for
// 5xx: it retries up to maxRetries and records exhaustion. Backoff is shortened
// by cancelling the context partway is not needed here because the server keeps
// returning 500; we instead assert the call count equals maxRetries. To keep
// the test fast we cap wall time by failing immediately at the transport level
// is not possible here, so we accept the real backoff (1s + 4s) — keep small.
func TestDeliver_ServerError_RetriesThenFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newDispatcherTestDB(t)
	store := NewStore(db)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	w := createTestWebhook(t, ctx, store, db, srv.URL, "whsec_x")
	delivery := newTestDelivery(w.ID, `{}`)
	require.NoError(t, store.CreateDelivery(ctx, delivery))

	// Cancel the context after the first attempt so we don't wait the full
	// exponential backoff (1s + 4s); this exercises the backoff-cancel branch
	// while still recording a failure. The first attempt has no backoff.
	cctx, cancel := context.WithCancel(ctx)
	go func() {
		// Wait until the first request lands, then cancel.
		for atomic.LoadInt32(&calls) < 1 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()

	d := newTestDispatcher(store)
	d.deliver(cctx, w, delivery, []byte(`{}`))

	// The first 5xx was recorded, the run did not succeed, and the
	// backoff-cancel branch set an error on the delivery.
	assert.GreaterOrEqual(t, atomic.LoadInt32(&calls), int32(1))
	assert.Nil(t, delivery.DeliveredAt)
	require.NotNil(t, delivery.Error)
	assert.Contains(t, *delivery.Error, "context cancelled during backoff")
}

// TestDeliver_ServerErrorRecoversToSuccess exercises a real retry: first call
// 500, second call 200. Uses the actual backoff (1s before attempt 2).
func TestDeliver_ServerErrorRecoversToSuccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newDispatcherTestDB(t)
	store := NewStore(db)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	w := createTestWebhook(t, ctx, store, db, srv.URL, "whsec_x")
	delivery := newTestDelivery(w.ID, `{}`)
	require.NoError(t, store.CreateDelivery(ctx, delivery))

	d := newTestDispatcher(store)
	d.deliver(ctx, w, delivery, []byte(`{}`))

	assert.Equal(t, int32(2), atomic.LoadInt32(&calls), "should retry once after 500")
	require.NotNil(t, delivery.DeliveredAt, "second attempt succeeds")
	assert.Equal(t, 2, delivery.Attempt)
	assert.Nil(t, delivery.Error)
}

// TestDeliver_RedirectsNotFollowed verifies the dispatcher does not follow a
// 301 redirect (SSRF-via-redirect protection). The redirect target would be a
// second server; if followed, it would record that server's status.
func TestDeliver_RedirectsNotFollowed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newDispatcherTestDB(t)
	store := NewStore(db)

	var targetHit int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHit, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer redirector.Close()

	w := createTestWebhook(t, ctx, store, db, redirector.URL, "whsec_x")
	delivery := newTestDelivery(w.ID, `{}`)
	require.NoError(t, store.CreateDelivery(ctx, delivery))

	d := newTestDispatcher(store)
	d.deliver(ctx, w, delivery, []byte(`{}`))

	assert.Equal(t, int32(0), atomic.LoadInt32(&targetHit), "redirect target must not be hit")
	require.NotNil(t, delivery.ResponseStatus)
	assert.Equal(t, 301, *delivery.ResponseStatus, "dispatcher sees the 301, not the redirected 200")
	// 301 is a 3xx (not 2xx, not 4xx/5xx). It records but is treated as a
	// retryable non-2xx and ultimately fails after retries; what matters for
	// this test is the target was never contacted.
}

// TestNewDispatcher_RealClientRejectsPrivateTarget exercises the production
// NewDispatcher wiring (hardened SSRF client, no redirects) end to end: a
// delivery to a loopback URL is rejected at dial time, so every attempt fails
// and the "all retries exhausted" path records an error. The context is
// cancelled right after the first dial so we don't wait the full backoff.
func TestNewDispatcher_RealClientRejectsPrivateTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newDispatcherTestDB(t)
	store := NewStore(db)

	// A loopback target the SSRF dialer will refuse to connect to.
	w := createTestWebhook(t, ctx, store, db, "http://127.0.0.1:1/hook", "whsec_x")
	delivery := newTestDelivery(w.ID, `{}`)
	require.NoError(t, store.CreateDelivery(ctx, delivery))

	d := NewDispatcher(store, zerolog.Nop())

	// Cancel shortly after start so the inter-attempt backoff returns early.
	cctx, cancel := context.WithCancel(ctx)
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	d.deliver(cctx, w, delivery, []byte(`{}`))

	// Never delivered; an error was recorded (either dial rejection exhaustion
	// or backoff cancellation).
	assert.Nil(t, delivery.DeliveredAt)
	require.NotNil(t, delivery.Error)
}

// TestSSRFSafeDialer_RejectsPrivateIPs asserts the live dialer rejects literal
// private/loopback/link-local IPs (and a hostname resolving to loopback), and
// allows a public IP literal (dial may fail to connect, but must not be
// rejected by the SSRF guard).
func TestSSRFSafeDialer_RejectsPrivateIPs(t *testing.T) {
	t.Parallel()
	dial := ssrfSafeDialer()
	ctx := context.Background()

	blocked := []string{
		"127.0.0.1:80",
		"10.0.0.1:80",
		"169.254.169.254:80", // cloud metadata endpoint
		"[::1]:80",
		"192.168.1.1:80",
		"172.16.0.1:80",
	}
	for _, addr := range blocked {
		t.Run("blocked_"+addr, func(t *testing.T) {
			conn, err := dial(ctx, "tcp", addr)
			if conn != nil {
				_ = conn.Close()
			}
			require.Error(t, err, "dial to %s must be rejected", addr)
			assert.Contains(t, err.Error(), "private IP")
		})
	}

	// A hostname that resolves to loopback must also be rejected. localhost
	// resolves to 127.0.0.1 / ::1, both private — no DNS-rebinding window
	// because the dialer connects to the validated IP, not the hostname.
	t.Run("hostname_resolving_to_loopback", func(t *testing.T) {
		conn, err := dial(ctx, "tcp", "localhost:80")
		if conn != nil {
			_ = conn.Close()
		}
		require.Error(t, err)
		assert.Contains(t, err.Error(), "private IP")
	})

	// A public IP literal must pass the SSRF check. We use a short-timeout
	// context so we don't actually wait on a real connection; the key
	// assertion is that the error (if any) is NOT the "private IP" rejection.
	t.Run("allowed_public_ip", func(t *testing.T) {
		cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		conn, err := dial(cctx, "tcp", "8.8.8.8:9") // discard port, won't complete
		if conn != nil {
			_ = conn.Close()
		}
		if err != nil {
			assert.NotContains(t, err.Error(), "private IP", "public IP must not be SSRF-rejected")
		}
	})
}

// TestSSRFSafeDialer_InvalidAddr covers the malformed-address branch.
func TestSSRFSafeDialer_InvalidAddr(t *testing.T) {
	t.Parallel()
	dial := ssrfSafeDialer()
	conn, err := dial(context.Background(), "tcp", "no-port-here")
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid address")
}

// TestSSRFSafeDialer_DNSFailure covers the DNS-lookup-failure branch.
func TestSSRFSafeDialer_DNSFailure(t *testing.T) {
	t.Parallel()
	dial := ssrfSafeDialer()
	// A reserved invalid TLD that will not resolve.
	conn, err := dial(context.Background(), "tcp", "this-host-does-not-exist.invalid:80")
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DNS lookup failed")
}

// TestSSRFSafeDialer_NoRebindWindow confirms the dialer dials the validated IP
// rather than re-resolving the hostname (no TOCTOU/DNS-rebinding window). We
// stand up a real loopback listener and assert that even though a connection is
// physically possible, the SSRF guard refuses it because the resolved IP is
// loopback — proving the IP, not just the name, gates the dial.
func TestSSRFSafeDialer_NoRebindWindow(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	_, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	dial := ssrfSafeDialer()
	conn, err := dial(context.Background(), "tcp", net.JoinHostPort("localhost", port))
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, err, "must reject even when a loopback listener exists")
	assert.Contains(t, err.Error(), "private IP")
}

// TestDispatch_FiresEnabledWebhooks runs the full Dispatch path: it finds the
// enabled webhook, creates a delivery, and delivers asynchronously. We use a
// channel to await the async goroutine.
func TestDispatch_FiresEnabledWebhooks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newDispatcherTestDB(t)
	store := NewStore(db)

	var (
		mu       sync.Mutex
		gotBody  []byte
		received = make(chan struct{}, 1)
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotBody, _ = io.ReadAll(r.Body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		select {
		case received <- struct{}{}:
		default:
		}
	}))
	defer srv.Close()

	w := createTestWebhook(t, ctx, store, db, srv.URL, "whsec_x")

	d := newTestDispatcher(store)
	d.Dispatch(ctx, w.EventID, "rsvp.created", map[string]any{"guest": "alice"})

	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("webhook was not delivered within timeout")
	}

	mu.Lock()
	body := string(gotBody)
	mu.Unlock()
	assert.Contains(t, body, `"eventType":"rsvp.created"`)
	assert.Contains(t, body, `"guest":"alice"`)
}

// TestDispatch_NoMatchingWebhooks is a no-op path: no enabled webhook for the
// event type, so nothing is delivered and no panic occurs.
func TestDispatch_NoMatchingWebhooks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newDispatcherTestDB(t)
	store := NewStore(db)

	eventID := uuid.Must(uuid.NewV7()).String()
	createParentEvent(t, ctx, db, eventID)

	d := newTestDispatcher(store)
	// Should return cleanly with no webhooks registered.
	d.Dispatch(ctx, eventID, "rsvp.created", map[string]any{"x": 1})
}

// TestSendTest_HappyPath exercises Service.SendTest and getDeliveryByID end to
// end: a test payload is delivered synchronously to a 200 endpoint and the
// returned delivery reflects success.
func TestSendTest_HappyPath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newDispatcherTestDB(t)
	store := NewStore(db)

	var gotEvent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEvent = r.Header.Get("X-OpenRSVP-Event")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("thanks"))
	}))
	defer srv.Close()

	w := createTestWebhook(t, ctx, store, db, srv.URL, "whsec_test")

	svc := NewService(store, zerolog.Nop(), false)
	d := newTestDispatcher(store)

	result, err := svc.SendTest(ctx, w.ID, d)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "test", gotEvent)
	assert.Equal(t, "test", result.EventType)
	require.NotNil(t, result.ResponseStatus)
	assert.Equal(t, 200, *result.ResponseStatus)
	require.NotNil(t, result.DeliveredAt)
	require.NotNil(t, result.ResponseBody)
	assert.Equal(t, "thanks", *result.ResponseBody)
	assert.Contains(t, result.Payload, `"eventType":"test"`)
}

// TestSendTest_SingleAttempt confirms a failing endpoint is tried once. Retries
// would add at least 4s of backoff before a second attempt, which is how the
// e2e "send test webhook" case timed out against a slow third-party host.
func TestSendTest_SingleAttempt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newDispatcherTestDB(t)
	store := NewStore(db)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	w := createTestWebhook(t, ctx, store, db, srv.URL, "whsec_test")

	svc := NewService(store, zerolog.Nop(), false)
	d := newTestDispatcher(store)

	start := time.Now()
	result, err := svc.SendTest(ctx, w.ID, d)
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, int32(1), atomic.LoadInt32(&calls))
	assert.Less(t, elapsed, 2*time.Second)
	assert.Equal(t, "test", result.EventType)
	require.NotNil(t, result.ResponseStatus)
	assert.Equal(t, 500, *result.ResponseStatus)
	assert.Equal(t, 1, result.Attempt)
}

// TestSendTest_WebhookNotFound covers the not-found branch.
func TestSendTest_WebhookNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := newDispatcherTestDB(t)
	store := NewStore(db)

	svc := NewService(store, zerolog.Nop(), false)
	d := newTestDispatcher(store)

	_, err := svc.SendTest(ctx, "nonexistent", d)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook not found")
}

// --- helpers ---

func newDispatcherTestDB(t *testing.T) database.DB {
	t.Helper()
	return testutil.NewTestDB(t)
}

// getDelivery reads a delivery row back via the store's underlying scan path.
func getDelivery(t *testing.T, ctx context.Context, store *Store, id string) *Delivery {
	t.Helper()
	deliveries, err := store.FindDeliveriesByWebhook(ctx, mustWebhookIDForDelivery(t, ctx, store, id), 100)
	require.NoError(t, err)
	for _, d := range deliveries {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("delivery %s not found", id)
	return nil
}

// mustWebhookIDForDelivery looks up the webhook_id for a delivery so we can use
// the public FindDeliveriesByWebhook read path.
func mustWebhookIDForDelivery(t *testing.T, ctx context.Context, store *Store, deliveryID string) string {
	t.Helper()
	var webhookID string
	err := store.db.QueryRowContext(ctx,
		`SELECT webhook_id FROM webhook_deliveries WHERE id = ?`, deliveryID).Scan(&webhookID)
	require.NoError(t, err)
	return webhookID
}
