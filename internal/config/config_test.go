package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadNotificationProviderEnv(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("ENV", "development")
	t.Setenv("DB_DRIVER", "sqlite")
	t.Setenv("DB_DSN", "openrsvp.db")
	t.Setenv("NOTIFICATION_EMAIL_PROVIDER", "smtp")
	t.Setenv("NOTIFICATION_SMS_PROVIDER", "twilio")
	t.Setenv("TWILIO_ACCOUNT_SID", "AC123")
	t.Setenv("TWILIO_AUTH_TOKEN", "token")
	t.Setenv("TWILIO_FROM_NUMBER", "+15551234567")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "smtp", cfg.NotificationEmailProvider)
	assert.Equal(t, "twilio", cfg.NotificationSMSProvider)
	assert.Equal(t, "AC123", cfg.TwilioAccountSID)
	assert.Equal(t, "token", cfg.TwilioAuthToken)
	assert.Equal(t, "+15551234567", cfg.TwilioFromNumber)
}
