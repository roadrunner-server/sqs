package sqsjobs

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/require"
)

func TestConfigDefaults(t *testing.T) {
	c := &Config{}
	c.InitDefault()

	require.Equal(t, "default", aws.ToString(c.Queue))
	require.Equal(t, int32(1), c.Prefetch)
	require.Equal(t, int32(1), c.MaxMsgInFlightLimit)
	require.Zero(t, c.WaitTimeSeconds)
	require.Zero(t, c.VisibilityTimeout)
	require.NotNil(t, c.Attributes)
	require.NotNil(t, c.Tags)
}

// TestConfigClampsToSQSBounds covers the limits the SQS api enforces: prefetch
// is capped at the 10 message receive maximum, visibility at 12 hours and the
// wait time at 20 seconds.
func TestConfigClampsToSQSBounds(t *testing.T) {
	c := &Config{
		Prefetch:               100,
		WaitTimeSeconds:        60,
		VisibilityTimeout:      50000,
		ErrorVisibilityTimeout: 50000,
	}
	c.InitDefault()

	require.Equal(t, int32(10), c.Prefetch)
	require.Equal(t, int32(20), c.WaitTimeSeconds)
	require.Equal(t, int32(43200), c.VisibilityTimeout)
	require.Equal(t, int32(43200), c.ErrorVisibilityTimeout)
}

func TestConfigNegativeValuesIgnored(t *testing.T) {
	c := &Config{Prefetch: -5, WaitTimeSeconds: -1, VisibilityTimeout: -1, ErrorVisibilityTimeout: -1}
	c.InitDefault()

	require.Equal(t, int32(1), c.Prefetch)
	require.Zero(t, c.WaitTimeSeconds)
	require.Zero(t, c.VisibilityTimeout)
	require.Zero(t, c.ErrorVisibilityTimeout)
}

// TestConfigTestEnvOverride covers the env override the e2e suite relies on.
func TestConfigTestEnvOverride(t *testing.T) {
	t.Setenv("RR_TEST_ENV", "local")
	t.Setenv("RR_SQS_TEST_REGION", "us-west-1")
	t.Setenv("RR_SQS_TEST_KEY", "key")
	t.Setenv("RR_SQS_TEST_SECRET", "secret")
	t.Setenv("RR_SQS_TEST_ENDPOINT", "http://127.0.0.1:4566")
	t.Setenv("RR_SQS_TEST_ACCOUNT_ID", "000000000000")

	c := &Config{Region: "eu-west-1", Key: "a", Secret: "b", Endpoint: "c"}
	c.InitDefault()

	require.Equal(t, "us-west-1", c.Region)
	require.Equal(t, "key", c.Key)
	require.Equal(t, "secret", c.Secret)
	require.Equal(t, "http://127.0.0.1:4566", c.Endpoint)
}

// TestConfigTestEnvIncomplete covers the fail fast guard: enabling test mode
// with a variable missing has to panic rather than dial a half-real endpoint.
func TestConfigTestEnvIncomplete(t *testing.T) {
	t.Setenv("RR_TEST_ENV", "local")
	t.Setenv("RR_SQS_TEST_REGION", "")

	require.Panics(t, func() { (&Config{}).InitDefault() })
}
