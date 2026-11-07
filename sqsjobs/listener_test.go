package sqsjobs

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"testing/synctest"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/stretchr/testify/require"
)

func TestListenCredentialsError(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "credentials unavailable", err: errors.New("credentials unavailable")},
		{name: "credentials timeout", err: context.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var logs bytes.Buffer
				driver := &Driver{
					log:      slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
					pauseCh:  make(chan struct{}),
					queueURL: aws.String("http://sqs.invalid/queue"),
					prefetch: 1,
				}
				driver.client = sqs.NewFromConfig(aws.Config{
					Region: "us-west-1",
					Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
						close(driver.pauseCh)
						return aws.Credentials{}, tt.err
					}),
					BaseEndpoint:     aws.String("http://sqs.invalid"),
					RetryMaxAttempts: 1,
				})

				driver.listen(t.Context())
				synctest.Wait()

				require.Contains(t, logs.String(), "receive message")
				require.Contains(t, logs.String(), tt.err.Error())
				require.Contains(t, logs.String(), "sqs listener was stopped")
			})
		})
	}
}
