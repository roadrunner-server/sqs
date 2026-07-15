package helpers

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	sqsConf "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	jobsProto "github.com/roadrunner-server/api-go/v6/jobs/v2"
	jobState "github.com/roadrunner-server/api-plugins/v6/jobs"
	goridgeRpc "github.com/roadrunner-server/goridge/v4/pkg/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	push     string = "jobs.Push"
	pause    string = "jobs.Pause"
	resume   string = "jobs.Resume"
	destroy  string = "jobs.Destroy"
	getStats string = "jobs.GetStats"
)

// NewJobsClient dials the RR RPC endpoint and returns a net/rpc client that
// speaks the goridge codec, the same transport the jobs plugin serves.
func NewJobsClient(t *testing.T, address string) *rpc.Client {
	t.Helper()
	// Dial with a background-derived context: NewJobsClient is also used from
	// t.Cleanup (e.g. DestroyPipelines), where t.Context() is already canceled.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	require.NoError(t, err)

	client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))
	// Close the client (and its underlying connection) when the test ends so a
	// fresh-client-per-call helper does not leak sockets/goroutines across a run.
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func DeleteQueues(t *testing.T, queueNames ...string) {
	// Use context.Background() rather than t.Context(): callers invoke
	// DeleteQueues from t.Cleanup, where t.Context() is already canceled.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	awsConf, err := sqsConf.LoadDefaultConfig(ctx,
		sqsConf.WithBaseEndpoint(os.Getenv("RR_SQS_TEST_ENDPOINT")),
		sqsConf.WithRegion(os.Getenv("RR_SQS_TEST_REGION")),
		sqsConf.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(os.Getenv("RR_SQS_TEST_KEY"),
			os.Getenv("RR_SQS_TEST_SECRET"), "")))
	require.NoError(t, err)

	client := sqs.NewFromConfig(awsConf, func(o *sqs.Options) {
		o.Retryer = retry.NewStandard(func(so *retry.StandardOptions) {
			so.MaxAttempts = 2
		})
	})

	for _, queueName := range queueNames {
		_, err := client.DeleteQueue(ctx, &sqs.DeleteQueueInput{
			QueueUrl: aws.String(fmt.Sprintf("%s/%s/%s",
				os.Getenv("RR_SQS_TEST_ENDPOINT"),
				os.Getenv("RR_SQS_TEST_ACCOUNT_ID"),
				queueName),
			),
		})

		assert.NoError(t, err)
	}
}

func ResumePipes(address string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		err := client.Call(resume, &jobsProto.Pipelines{Pipelines: slices.Clone(pipes)}, &jobsProto.JobsHandlerResponse{})
		require.NoError(t, err)
	}
}

func PushToPipe(pipeline string, autoAck bool, address string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		err := client.Call(push, &jobsProto.PushRequest{Job: createDummyJob(pipeline, autoAck)}, &jobsProto.JobsHandlerResponse{})
		require.NoError(t, err)
	}
}

func PushToPipeDelayed(address string, pipeline string, delay int64) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		req := &jobsProto.PushRequest{Job: &jobsProto.Job{
			Job:     "some/php/namespace",
			Id:      uuid.NewString(),
			Payload: []byte(`{"hello":"world"}`),
			Headers: map[string]*jobsProto.JobHeaderValue{"test": {Values: []string{"test2"}}},
			Options: &jobsProto.Options{
				Priority: 1,
				Pipeline: pipeline,
				Delay:    delay,
			},
		}}
		err := client.Call(push, req, &jobsProto.JobsHandlerResponse{})
		assert.NoError(t, err)
	}
}

func createDummyJob(pipeline string, autoAck bool) *jobsProto.Job {
	return &jobsProto.Job{
		Job:     "some/php/namespace",
		Id:      uuid.NewString(),
		Payload: []byte(`{"hello":"world"}`),
		Headers: map[string]*jobsProto.JobHeaderValue{"test": {Values: []string{"test2"}}},
		Options: &jobsProto.Options{
			AutoAck:  autoAck,
			Priority: 1,
			Pipeline: pipeline,
			Topic:    pipeline,
		},
	}
}

func PausePipelines(address string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		err := client.Call(pause, &jobsProto.Pipelines{Pipelines: slices.Clone(pipes)}, &jobsProto.JobsHandlerResponse{})
		assert.NoError(t, err)
	}
}

func DestroyPipelines(address string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		req := &jobsProto.Pipelines{Pipelines: slices.Clone(pipes)}

		// Retry the destroy 10× with 1s gaps; if all attempts fail, return
		// without asserting. Some negative tests intentionally destroy
		// non-existent pipelines and rely on this silent-after-retry pattern.
		for range 10 {
			if err := client.Call(destroy, req, &jobsProto.Pipelines{}); err == nil {
				return
			}
			time.Sleep(time.Second)
		}
	}
}

func Stats(address string, state *jobState.State) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)

		out := &jobsProto.Stats{}
		err := client.Call(getStats, &emptypb.Empty{}, out)
		require.NoError(t, err)
		require.NotEmpty(t, out.GetStats())

		st := out.GetStats()[0]
		state.Queue = st.GetQueue()
		state.Pipeline = st.GetPipeline()
		state.Driver = st.GetDriver()
		state.Active = st.GetActive()
		state.Delayed = st.GetDelayed()
		state.Reserved = st.GetReserved()
		state.Ready = st.GetReady()
		state.Priority = st.GetPriority()
	}
}

func setProxy(name string, enabled bool, t *testing.T) {
	t.Helper()
	body := strings.NewReader(`{"enabled":` + boolStr(enabled) + `}`)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:8474/proxies/"+name, body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func EnableProxy(name string, t *testing.T) {
	setProxy(name, true, t)
}

func DisableProxy(name string, t *testing.T) {
	setProxy(name, false, t)
}

func DeleteProxy(name string, t *testing.T) {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, "http://127.0.0.1:8474/proxies/"+name, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, 204, resp.StatusCode)
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
}
