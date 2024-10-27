package helpers

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/retry"

	"github.com/aws/aws-sdk-go-v2/aws"
	sqsConf "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	jobsProto "github.com/roadrunner-server/api/v4/build/jobs/v1"
	jobState "github.com/roadrunner-server/api/v4/plugins/v4/jobs"
	goridgeRpc "github.com/roadrunner-server/goridge/v3/pkg/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	push    string = "jobs.Push"
	pause   string = "jobs.Pause"
	destroy string = "jobs.Destroy"
	resume  string = "jobs.Resume"
	stat    string = "jobs.Stat"
)

func DeleteQueues(t *testing.T, queueNames ...string) {
	// should not be more than 1 minute
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
		conn, err := net.Dial("tcp", address)
		require.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		pipe := &jobsProto.Pipelines{Pipelines: make([]string, len(pipes))}

		for i := 0; i < len(pipes); i++ {
			pipe.GetPipelines()[i] = pipes[i]
		}

		er := &jobsProto.Empty{}
		err = client.Call(resume, pipe, er)
		require.NoError(t, err)
	}
}

func PushToPipe(pipeline string, autoAck bool, address string) func(t *testing.T) {
	return func(t *testing.T) {
		conn, err := net.Dial("tcp", address)
		require.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		req := &jobsProto.PushRequest{Job: createDummyJob(pipeline, autoAck)}

		er := &jobsProto.Empty{}
		err = client.Call(push, req, er)
		require.NoError(t, err)
	}
}

func PushToPipeDelayed(address string, pipeline string, delay int64) func(t *testing.T) {
	return func(t *testing.T) {
		conn, err := net.Dial("tcp", address)
		assert.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		req := &jobsProto.PushRequest{Job: &jobsProto.Job{
			Job:     "some/php/namespace",
			Id:      uuid.NewString(),
			Payload: []byte(`{"hello":"world"}`),
			Headers: map[string]*jobsProto.HeaderValue{"test": {Value: []string{"test2"}}},
			Options: &jobsProto.Options{
				Priority: 1,
				Pipeline: pipeline,
				Delay:    delay,
			},
		}}

		er := &jobsProto.Empty{}
		err = client.Call(push, req, er)
		assert.NoError(t, err)
	}
}

func createDummyJob(pipeline string, autoAck bool) *jobsProto.Job {
	return &jobsProto.Job{
		Job:     "some/php/namespace",
		Id:      uuid.NewString(),
		Payload: []byte(`{"hello":"world"}`),
		Headers: map[string]*jobsProto.HeaderValue{"test": {Value: []string{"test2"}}},
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
		conn, err := net.Dial("tcp", address)
		assert.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		pipe := &jobsProto.Pipelines{Pipelines: make([]string, len(pipes))}

		for i := 0; i < len(pipes); i++ {
			pipe.GetPipelines()[i] = pipes[i]
		}

		er := &jobsProto.Empty{}
		err = client.Call(pause, pipe, er)
		assert.NoError(t, err)
	}
}

func DestroyPipelines(address string, pipes ...string) func(t *testing.T) {
	return func(t *testing.T) {
		conn, err := net.Dial("tcp", address)
		assert.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		pipe := &jobsProto.Pipelines{Pipelines: make([]string, len(pipes))}

		for i := 0; i < len(pipes); i++ {
			pipe.GetPipelines()[i] = pipes[i]
		}

		for i := 0; i < 10; i++ {
			er := &jobsProto.Empty{}
			err = client.Call(destroy, pipe, er)
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
			assert.NoError(t, err)
			break
		}
	}
}

func Stats(address string, state *jobState.State) func(t *testing.T) {
	return func(t *testing.T) {
		conn, err := net.Dial("tcp", address)
		require.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		st := &jobsProto.Stats{}
		er := &jobsProto.Empty{}

		err = client.Call(stat, er, st)
		require.NoError(t, err)
		require.NotNil(t, st)

		state.Queue = st.Stats[0].Queue
		state.Pipeline = st.Stats[0].Pipeline
		state.Driver = st.Stats[0].Driver
		state.Active = st.Stats[0].Active
		state.Delayed = st.Stats[0].Delayed
		state.Reserved = st.Stats[0].Reserved
		state.Ready = st.Stats[0].Ready
		state.Priority = st.Stats[0].Priority
	}
}

func EnableProxy(name string, t *testing.T) {
	buf := new(bytes.Buffer)
	buf.WriteString(`{"enabled":true}`)

	resp, err := http.Post("http://127.0.0.1:8474/proxies/"+name, "application/json", buf) //nolint:noctx
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
}

func DisableProxy(name string, t *testing.T) {
	buf := new(bytes.Buffer)
	buf.WriteString(`{"enabled":false}`)

	resp, err := http.Post("http://127.0.0.1:8474/proxies/"+name, "application/json", buf) //nolint:noctx
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
}

func DeleteProxy(name string, t *testing.T) {
	client := &http.Client{}

	req, err := http.NewRequest(http.MethodDelete, "http://127.0.0.1:8474/proxies/"+name, nil) //nolint:noctx
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	require.NoError(t, err)
	require.Equal(t, 204, resp.StatusCode)
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
}
