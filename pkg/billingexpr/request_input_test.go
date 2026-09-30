package billingexpr

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeRequestInputRemovesCredentialHeaders(t *testing.T) {
	input := RequestInput{
		Headers: map[string]string{
			"Authorization":       "Bearer test-secret",
			"X-Api-Key":           "api-secret",
			"X-Trace-Token":       "trace-secret",
			"Cookie":              "session-secret",
			"Content-Type":        "application/json",
			"X-Billing-Dimension": "video",
		},
		Body: []byte(`{"duration":4}`),
	}

	sanitized := SanitizeRequestInput(input)
	require.NotContains(t, sanitized.Headers, "Authorization")
	require.NotContains(t, sanitized.Headers, "X-Api-Key")
	require.NotContains(t, sanitized.Headers, "X-Trace-Token")
	require.NotContains(t, sanitized.Headers, "Cookie")
	require.Equal(t, "application/json", sanitized.Headers["Content-Type"])
	require.Equal(t, "video", sanitized.Headers["X-Billing-Dimension"])
	require.Equal(t, input.Body, sanitized.Body)

	input.Headers["Content-Type"] = "text/plain"
	input.Body[0] = 'X'
	require.Equal(t, "application/json", sanitized.Headers["Content-Type"])
	require.Equal(t, byte('{'), sanitized.Body[0])
}
