package notification

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yannkr/openrsvp/internal/testutil"
)

// captureProvider is a test email provider that records the last message it
// was asked to send and returns a fixed message ID.
type captureProvider struct {
	last      *Message
	messageID string
}

func (p *captureProvider) Name() string     { return "capture" }
func (p *captureProvider) Channel() Channel { return ChannelEmail }
func (p *captureProvider) Send(_ context.Context, msg *Message) (*SendResult, error) {
	// Copy so later mutation by the service does not race the assertion.
	cp := *msg
	p.last = &cp
	return &SendResult{MessageID: p.messageID}, nil
}
func (p *captureProvider) SendBatch(ctx context.Context, msgs []*Message) ([]*SendResult, []error) {
	results := make([]*SendResult, len(msgs))
	errs := make([]error, len(msgs))
	for i, m := range msgs {
		results[i], errs[i] = p.Send(ctx, m)
	}
	return results, errs
}
func (p *captureProvider) HealthCheck(context.Context) error { return nil }

// fakeSuppression is a test SuppressionChecker.
type fakeSuppression struct {
	suppressed map[string]bool // email -> suppressed
	tokens     map[string]string
}

func newFakeSuppression() *fakeSuppression {
	return &fakeSuppression{
		suppressed: map[string]bool{},
		tokens:     map[string]string{},
	}
}

func (f *fakeSuppression) IsSuppressed(_ context.Context, email, _ string) bool {
	return f.suppressed[email]
}

func (f *fakeSuppression) GenerateUnsubscribeToken(_ context.Context, email, _ string) (string, error) {
	tok := "tok-" + email
	f.tokens[email] = tok
	return tok, nil
}

func newTestRegistry(p Provider) *Registry {
	r := NewRegistry()
	r.Register(p)
	return r
}

func TestService_OpenPixel_EmbeddedWhenEnabled(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	eventID := uuid.Must(uuid.NewV7()).String()
	attendeeID := uuid.Must(uuid.NewV7()).String()
	createParentRecordsForNotification(t, ctx, db, eventID, attendeeID)

	prov := &captureProvider{messageID: "mid-1"}
	svc := NewServiceWithOptions(newTestRegistry(prov), db, zerolog.Nop(), Options{
		BaseURL:             "https://rsvp.example.com",
		OpenTrackingEnabled: true,
	})

	err := svc.Send(ctx, eventID, attendeeID, ChannelEmail, &Message{
		To:      "alice@example.com",
		Subject: "Hi",
		Body:    "<html><body><p>Hello</p></body></html>",
		Plain:   "Hello",
	})
	require.NoError(t, err)
	require.NotNil(t, prov.last)

	assert.Contains(t, prov.last.Body, "/api/v1/notifications/track/open/")
	assert.Contains(t, prov.last.Body, `width="1" height="1"`)
	// Plain text part must NOT get a pixel.
	assert.NotContains(t, prov.last.Plain, "track/open")

	// The pixel id must be the notification_log row id so RecordOpen works.
	var logID string
	err = db.QueryRowContext(ctx, "SELECT id FROM notification_log WHERE event_id = ?", eventID).Scan(&logID)
	require.NoError(t, err)
	assert.Contains(t, prov.last.Body, "/track/open/"+logID)
}

func TestService_OpenPixel_AbsentWhenDisabled(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	eventID := uuid.Must(uuid.NewV7()).String()
	attendeeID := uuid.Must(uuid.NewV7()).String()
	createParentRecordsForNotification(t, ctx, db, eventID, attendeeID)

	prov := &captureProvider{messageID: "mid-2"}
	svc := NewServiceWithOptions(newTestRegistry(prov), db, zerolog.Nop(), Options{
		BaseURL:             "https://rsvp.example.com",
		OpenTrackingEnabled: false, // disabled
	})

	err := svc.Send(ctx, eventID, attendeeID, ChannelEmail, &Message{
		To:      "alice@example.com",
		Subject: "Hi",
		Body:    "<html><body><p>Hello</p></body></html>",
	})
	require.NoError(t, err)
	require.NotNil(t, prov.last)
	assert.NotContains(t, prov.last.Body, "track/open")
}

func TestService_SuppressionGate_SkipsSuppressed(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	eventID := uuid.Must(uuid.NewV7()).String()
	attendeeID := uuid.Must(uuid.NewV7()).String()
	createParentRecordsForNotification(t, ctx, db, eventID, attendeeID)

	fake := newFakeSuppression()
	fake.suppressed["blocked@example.com"] = true

	prov := &captureProvider{messageID: "mid-3"}
	svc := NewServiceWithOptions(newTestRegistry(prov), db, zerolog.Nop(), Options{
		BaseURL:     "https://rsvp.example.com",
		Suppression: fake,
	})

	err := svc.Send(ctx, eventID, attendeeID, ChannelEmail, &Message{
		To:      "blocked@example.com",
		Subject: "Hi",
		Body:    "<html><body>Hello</body></html>",
	})
	require.NoError(t, err)
	assert.Nil(t, prov.last, "suppressed recipient must not be sent to")

	// No notification_log row should have been written for the skipped send.
	var count int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM notification_log WHERE event_id = ?", eventID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestService_UnsubscribeFooter_AddedWithSuppression(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	eventID := uuid.Must(uuid.NewV7()).String()
	attendeeID := uuid.Must(uuid.NewV7()).String()
	createParentRecordsForNotification(t, ctx, db, eventID, attendeeID)

	fake := newFakeSuppression()
	prov := &captureProvider{messageID: "mid-4"}
	svc := NewServiceWithOptions(newTestRegistry(prov), db, zerolog.Nop(), Options{
		BaseURL:     "https://rsvp.example.com",
		Suppression: fake,
	})

	err := svc.Send(ctx, eventID, attendeeID, ChannelEmail, &Message{
		To:      "alice@example.com",
		Subject: "Hi",
		Body:    "<html><body><p>Hi</p></body></html>",
		Plain:   "Hi",
	})
	require.NoError(t, err)
	require.NotNil(t, prov.last)
	assert.Contains(t, prov.last.Body, "/unsubscribe?token=tok-alice@example.com")
	assert.Contains(t, prov.last.Plain, "/unsubscribe?token=tok-alice@example.com")
}

func TestService_UnsubscribeFooter_AbsentWithoutSuppression(t *testing.T) {
	db := testutil.NewTestDB(t)
	ctx := context.Background()
	eventID := uuid.Must(uuid.NewV7()).String()
	attendeeID := uuid.Must(uuid.NewV7()).String()
	createParentRecordsForNotification(t, ctx, db, eventID, attendeeID)

	prov := &captureProvider{messageID: "mid-5"}
	svc := NewServiceWithOptions(newTestRegistry(prov), db, zerolog.Nop(), Options{
		BaseURL: "https://rsvp.example.com",
	})

	err := svc.Send(ctx, eventID, attendeeID, ChannelEmail, &Message{
		To:   "alice@example.com",
		Body: "<html><body>Hi</body></html>",
	})
	require.NoError(t, err)
	require.NotNil(t, prov.last)
	assert.NotContains(t, prov.last.Body, "unsubscribe")
}
