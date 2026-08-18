package helpers

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/rpc"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	sqsConf "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	jobsProto "github.com/roadrunner-server/api-go/v6/jobs/v1"
	jobState "github.com/roadrunner-server/api-plugins/v6/jobs"
	goridgeRpc "github.com/roadrunner-server/goridge/v4/pkg/rpc"
	"github.com/stretchr/testify/require"
)

const (
	// toxiproxyAddr is the toxiproxy api used by the durability test.
	toxiproxyAddr = "127.0.0.1:8474"
	// redialTimeout bounds PushEventually, which retries across an outage.
	redialTimeout = time.Second * 60
	redialTick    = time.Second
)

func NewJobsClient(t *testing.T, address string) *rpc.Client {
	t.Helper()

	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", address)
	require.NoError(t, err)

	client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))
	t.Cleanup(func() { _ = client.Close() })

	return client
}

func ResumePipes(address string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		require.NoError(t, client.Call("jobs.Resume",
			&jobsProto.Pipelines{Pipelines: slices.Clone(pipes)},
			&jobsProto.Empty{}))
	}
}

func PausePipelines(address string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		require.NoError(t, client.Call("jobs.Pause",
			&jobsProto.Pipelines{Pipelines: slices.Clone(pipes)},
			&jobsProto.Empty{}))
	}
}

func DestroyPipelines(address string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		require.NoError(t, client.Call("jobs.Destroy",
			&jobsProto.Pipelines{Pipelines: slices.Clone(pipes)},
			&jobsProto.Pipelines{}))
	}
}

func PushToPipe(pipeline string, autoAck bool, address string) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		require.NoError(t, client.Call("jobs.Push",
			&jobsProto.PushRequest{Job: dummyJob(pipeline, autoAck, 0)},
			&jobsProto.Empty{}))
	}
}

func PushToPipeDelayed(address string, pipeline string, delay int64) func(t *testing.T) {
	return func(t *testing.T) {
		client := NewJobsClient(t, address)
		require.NoError(t, client.Call("jobs.Push",
			&jobsProto.PushRequest{Job: dummyJob(pipeline, false, delay)},
			&jobsProto.Empty{}))
	}
}

// PushEventually keeps retrying a push until it lands. Used after a broker
// outage, where the driver needs a few attempts to get through again.
func PushEventually(t *testing.T, address string, pipeline string) {
	t.Helper()

	require.Eventually(t, func() bool {
		client := NewJobsClient(t, address)

		return client.Call("jobs.Push",
			&jobsProto.PushRequest{Job: dummyJob(pipeline, false, 0)},
			&jobsProto.Empty{}) == nil
	}, redialTimeout, redialTick, "the driver never recovered after the outage")
}

func dummyJob(pipeline string, autoAck bool, delay int64) *jobsProto.Job {
	return &jobsProto.Job{
		Job:     "some/php/namespace",
		Id:      uuid.NewString(),
		Payload: []byte(`{"hello":"world"}`),
		Headers: map[string]*jobsProto.HeaderValue{"test": {Value: []string{"test2"}}},
		Options: &jobsProto.Options{
			AutoAck:  autoAck,
			Priority: 1,
			Pipeline: pipeline,
			Delay:    delay,
		},
	}
}

// DeclarePipe declares a pipeline over rpc and requires the call to succeed.
// Each call gets its own queue, so a test never inherits messages from another.
func DeclarePipe(address string, pipeline string, queue string) func(t *testing.T) {
	return func(t *testing.T) {
		require.NoError(t, Declare(t, address, map[string]string{
			"driver":             "sqs",
			"name":               pipeline,
			"queue":              queue,
			"prefetch":           "10",
			"priority":           "3",
			"visibility_timeout": "0",
			"wait_time_seconds":  "3",
			"tags":               `{"key":"value"}`,
		}))

		t.Cleanup(func() { DeleteQueues(t, queue) })
	}
}

// Declare issues a raw declare call and returns its error, so negative tests
// can assert on a rejected pipeline configuration.
func Declare(t *testing.T, address string, pipeline map[string]string) error {
	t.Helper()

	client := NewJobsClient(t, address)

	return client.Call("jobs.Declare",
		&jobsProto.DeclareRequest{Pipeline: pipeline},
		&jobsProto.Empty{})
}

// StatsFor returns the state the jobs plugin reports for one pipeline. Picking
// it by name keeps the assertion stable when several are registered.
func StatsFor(t *testing.T, address string, pipeline string) *jobState.State {
	t.Helper()

	resp := &jobsProto.Stats{}
	require.NoError(t, NewJobsClient(t, address).Call("jobs.Stat", &jobsProto.Empty{}, resp))

	for _, st := range resp.GetStats() {
		if st.GetPipeline() != pipeline {
			continue
		}

		return &jobState.State{
			Queue:    st.GetQueue(),
			Pipeline: st.GetPipeline(),
			Driver:   st.GetDriver(),
			Active:   st.GetActive(),
			Delayed:  st.GetDelayed(),
			Reserved: st.GetReserved(),
			Ready:    st.GetReady(),
			Priority: st.GetPriority(),
		}
	}

	require.FailNowf(t, "pipeline not reported", "no stats for %q", pipeline)

	return nil
}

// newSQSClient dials the endpoint directly, bypassing RoadRunner. The standard
// AWS env chain supplies the endpoint, region and credentials, the same way the
// driver resolves them when the config leaves them out.
func newSQSClient(ctx context.Context, t *testing.T) *sqs.Client {
	t.Helper()

	awsConf, err := sqsConf.LoadDefaultConfig(ctx)
	require.NoError(t, err)

	return sqs.NewFromConfig(awsConf, func(o *sqs.Options) {
		o.Retryer = retry.NewStandard(func(so *retry.StandardOptions) {
			so.MaxAttempts = 2
		})
	})
}

// queueURL resolves the url for a queue name through the api rather than by
// assembling it, so the tests do not care how the backend styles its urls.
func queueURL(ctx context.Context, t *testing.T, client *sqs.Client, queue string) *string {
	t.Helper()

	out, err := client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(queue)})
	require.NoError(t, err)

	return out.QueueUrl
}

// SendRaw puts a message on the queue without going through RoadRunner, so a
// test can hand the listener attributes the driver did not produce.
func SendRaw(t *testing.T, queue string, body string, attributes map[string]types.MessageAttributeValue) {
	t.Helper()

	client := newSQSClient(t.Context(), t)

	_, err := client.SendMessage(t.Context(), &sqs.SendMessageInput{
		QueueUrl:          queueURL(t.Context(), t, client, queue),
		MessageBody:       aws.String(body),
		MessageAttributes: attributes,
	})
	require.NoError(t, err)
}

// DeleteQueues drops the queues a test created. Runs from t.Cleanup, where the
// test context is already canceled.
func DeleteQueues(t *testing.T, queueNames ...string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	client := newSQSClient(ctx, t)
	for _, queueName := range queueNames {
		_, err := client.DeleteQueue(ctx, &sqs.DeleteQueueInput{QueueUrl: queueURL(ctx, t, client, queueName)})
		require.NoError(t, err)
	}
}

// CreateProxy fronts the queue endpoint with a toxiproxy the durability test
// can cut. Both addresses are resolved inside the compose network.
func CreateProxy(t *testing.T, name string, listen string, upstream string) {
	t.Helper()

	// a proxy left behind by an interrupted run would make the create conflict
	deleteProxy(t, name)

	body := fmt.Sprintf(`{"name":%q,"listen":%q,"upstream":%q,"enabled":true}`, name, listen, upstream)
	post(t, "http://"+toxiproxyAddr+"/proxies", []byte(body), http.StatusCreated)
	t.Cleanup(func() { deleteProxy(t, name) })
}

// SetProxyEnabled cuts or restores the connection to the endpoint.
func SetProxyEnabled(t *testing.T, name string, enabled bool) {
	t.Helper()

	post(t, "http://"+toxiproxyAddr+"/proxies/"+name, fmt.Appendf(nil, `{"enabled":%t}`, enabled), http.StatusOK)
}

func deleteProxy(t *testing.T, name string) {
	t.Helper()

	// runs from t.Cleanup, where the test context is already canceled
	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, "http://"+toxiproxyAddr+"/proxies/"+name, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Contains(t, []int{http.StatusNoContent, http.StatusNotFound}, resp.StatusCode)
}

func post(t *testing.T, addr string, body []byte, wantStatus int) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, addr, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, wantStatus, resp.StatusCode, "POST %s", addr)
}
