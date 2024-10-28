package sqs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"testing"
	"time"

	"tests/helpers"
	mocklogger "tests/mock"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	sqsConf "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/goccy/go-json"
	jobsProto "github.com/roadrunner-server/api/v4/build/jobs/v1"
	jobState "github.com/roadrunner-server/api/v4/plugins/v4/jobs"
	"github.com/roadrunner-server/config/v5"
	"github.com/roadrunner-server/endure/v2"
	goridgeRpc "github.com/roadrunner-server/goridge/v3/pkg/rpc"
	"github.com/roadrunner-server/informer/v5"
	"github.com/roadrunner-server/jobs/v5"
	"github.com/roadrunner-server/logger/v5"
	"github.com/roadrunner-server/otel/v5"
	"github.com/roadrunner-server/resetter/v5"
	rpcPlugin "github.com/roadrunner-server/rpc/v5"
	"github.com/roadrunner-server/server/v5"
	sqsPlugin "github.com/roadrunner-server/sqs/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSQSInit(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-sqs-init.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&logger.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqsPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	if err != nil {
		t.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}
	wg.Add(1)

	stopCh := make(chan struct{}, 1)

	go func() {
		defer wg.Done()
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	}()

	time.Sleep(time.Second * 3)
	t.Run("PushPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	t.Run("PushPipeline", helpers.PushToPipe("test-2", false, "127.0.0.1:6001"))
	time.Sleep(time.Second * 2)

	t.Run("DestroyPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-1", "test-2"))

	stopCh <- struct{}{}
	wg.Wait()

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "sqs-init-1", "sqs-init-2")
	})
}

func TestSQSRemovePQ(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.2.0",
		Path:    "configs/.rr-sqs-pq.yaml",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		l,
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqsPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	if err != nil {
		t.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}
	wg.Add(1)

	stopCh := make(chan struct{}, 1)

	go func() {
		defer wg.Done()
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	}()

	time.Sleep(time.Second * 3)

	for i := 0; i < 10; i++ {
		t.Run("PushPipeline", helpers.PushToPipe("test-1-pq", false, "127.0.0.1:6601"))
		t.Run("PushPipeline", helpers.PushToPipe("test-2-pq", false, "127.0.0.1:6601"))
	}
	time.Sleep(time.Second * 3)

	t.Run("DestroyPipeline", helpers.DestroyPipelines("127.0.0.1:6601", "test-1-pq", "test-2-pq"))

	stopCh <- struct{}{}
	wg.Wait()

	assert.Equal(t, 0, oLogger.FilterMessageSnippet("job was processed successfully").Len())
	assert.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was started").Len())
	assert.Equal(t, 2, oLogger.FilterMessageSnippet("pipeline was stopped").Len())
	assert.Equal(t, 20, oLogger.FilterMessageSnippet("job was pushed successfully").Len())
	assert.Equal(t, 4, oLogger.FilterMessageSnippet("job processing was started").Len())
	assert.Equal(t, 2, oLogger.FilterMessageSnippet("sqs listener was stopped").Len())

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "sqs-pq-1", "sqs-pq-2")
	})
}

func TestSQSAutoAck(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-sqs-auto-ack.yaml",
	}

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "sqs-auto-ack-1", "sqs-auto-ack-2")
	})

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		l,
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqsPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	if err != nil {
		t.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}
	wg.Add(1)

	stopCh := make(chan struct{}, 1)

	go func() {
		defer wg.Done()
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	}()

	time.Sleep(time.Second * 3)
	t.Run("PushPipeline", helpers.PushToPipe("test-1", true, "127.0.0.1:6001"))
	t.Run("PushPipeline", helpers.PushToPipe("test-1", true, "127.0.0.1:6001"))
	t.Run("PushPipeline", helpers.PushToPipe("test-1", true, "127.0.0.1:6001"))
	t.Run("PushPipeline", helpers.PushToPipe("test-2", true, "127.0.0.1:6001"))
	t.Run("PushPipeline", helpers.PushToPipe("test-2", true, "127.0.0.1:6001"))
	t.Run("PushPipeline", helpers.PushToPipe("test-2", true, "127.0.0.1:6001"))
	time.Sleep(time.Second * 10)
	t.Run("DestroyPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-1", "test-2"))

	stopCh <- struct{}{}
	wg.Wait()

	assert.Equal(t, 6, oLogger.FilterMessageSnippet("auto ack is turned on, message acknowledged").Len())
}

func TestSQSInitAttributes(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Path:    "configs/.rr-sqs-attr.yaml",
		Version: "2023.3.0",
	}

	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&logger.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqsPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	if err != nil {
		t.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}
	wg.Add(1)

	stopCh := make(chan struct{}, 1)

	go func() {
		defer wg.Done()
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	}()

	time.Sleep(time.Second * 3)
	t.Run("PushPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	t.Run("PushPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)
	t.Run("DestroyPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-1"))

	stopCh <- struct{}{}
	wg.Wait()

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "sqs-attr.fifo")
	})
}

func TestSQSInitBadResp(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Path:    "configs/.rr-sqs-init-br.yaml",
		Version: "2023.3.0",
	}

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "sqs-init-br-1", "sqs-init-br-2")
	})

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		l,
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqsPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	if err != nil {
		t.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}
	wg.Add(1)

	stopCh := make(chan struct{}, 1)

	go func() {
		defer wg.Done()
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	}()

	time.Sleep(time.Second * 3)
	t.Run("PushPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	t.Run("PushPipeline", helpers.PushToPipe("test-2", false, "127.0.0.1:6001"))
	t.Run("PushPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	t.Run("PushPipeline", helpers.PushToPipe("test-2", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)
	t.Run("DestroyPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-1", "test-2"))

	stopCh <- struct{}{}
	wg.Wait()

	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("response handler error").Len(), 2)
}

func TestSQSDeclare(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-sqs-declare.yaml",
	}

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "sqs-declare_test")
	})

	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&logger.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqsPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	if err != nil {
		t.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}
	wg.Add(1)

	stopCh := make(chan struct{}, 1)

	go func() {
		defer wg.Done()
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	}()

	time.Sleep(time.Second * 3)

	t.Run("DeclarePipeline", declareSQSPipe("sqs-declare_test", "127.0.0.1:6001", "test-3"))
	t.Run("ConsumePipeline", helpers.ResumePipes("127.0.0.1:6001", "test-3"))
	t.Run("PushPipeline", helpers.PushToPipe("test-3", false, "127.0.0.1:6001"))
	time.Sleep(time.Second)
	t.Run("PausePipeline", helpers.PausePipelines("127.0.0.1:6001", "test-3"))
	time.Sleep(time.Second)
	t.Run("DestroyPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-3"))

	time.Sleep(time.Second * 5)
	stopCh <- struct{}{}
	wg.Wait()
}

func TestSQSJobsError(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-sqs-jobs-err.yaml",
	}

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "declare_test_error")
	})

	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&logger.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqsPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	if err != nil {
		t.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}
	wg.Add(1)

	stopCh := make(chan struct{}, 1)

	go func() {
		defer wg.Done()
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	}()

	time.Sleep(time.Second * 3)

	t.Run("DeclarePipeline", declareSQSPipe("declare_test_error", "127.0.0.1:6001", "test-3"))
	t.Run("ConsumePipeline", helpers.ResumePipes("127.0.0.1:6001", "test-3"))
	t.Run("PushPipeline", helpers.PushToPipe("test-3", false, "127.0.0.1:6001"))
	time.Sleep(time.Second * 25)
	t.Run("PausePipeline", helpers.PausePipelines("127.0.0.1:6001", "test-3"))
	time.Sleep(time.Second)
	t.Run("DestroyPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-3"))

	time.Sleep(time.Second * 5)
	stopCh <- struct{}{}
	wg.Wait()
}

func TestSQSApproximateReceiveCount(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-sqs-read-approximate-count.yaml",
	}

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "sqs-read-approximate-count")
	})

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		l,
		&logger.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqsPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	if err != nil {
		t.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}
	wg.Add(1)

	stopCh := make(chan struct{}, 1)

	go func() {
		defer wg.Done()
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	}()

	time.Sleep(time.Second * 3)

	address := "127.0.0.1:6081"
	pipe := "test-err-approx-count"

	// Push a job to the pipeline, wait for it to be consumed 4 times, which should take ~30
	// In these 30 seconds, it should post Receive count: n for each attempt.
	t.Run("PushPipeline", helpers.PushToPipe(pipe, false, address))

	// 5s grace period
	time.Sleep(time.Second * 35)

	// Stop consuming messages
	t.Run("PausePipeline", helpers.PausePipelines(address, pipe))

	// Ensure that we can find a message saying the job was received 4 times after ~30s
	// First receive is at 0 seconds, second at ~10, third at ~20 and fourth at ~30
	assert.Equal(t, 1, oLogger.FilterMessageSnippet("Receive count: 4").Len())

	stopCh <- struct{}{}
	wg.Wait()
}

func TestSQSStat(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-sqs-stat.yaml",
	}

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "sqs-test-declare-stats")
	})

	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&logger.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqsPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	if err != nil {
		t.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}
	wg.Add(1)

	stopCh := make(chan struct{}, 1)

	go func() {
		defer wg.Done()
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	}()

	time.Sleep(time.Second * 3)

	address := "127.0.0.1:6010"
	pipe := "default-stat"
	queue := "sqs-test-declare-stats"

	t.Run("DeclarePipeline", declareSQSPipe(queue, address, pipe))
	t.Run("ConsumePipeline", helpers.ResumePipes(address, pipe))
	t.Run("PushPipeline", helpers.PushToPipe(pipe, false, address))
	time.Sleep(time.Second)
	t.Run("PausePipeline", helpers.PausePipelines(address, pipe))
	time.Sleep(time.Second)

	t.Run("PushPipelineDelayed", helpers.PushToPipeDelayed(address, pipe, 5))
	t.Run("PushPipeline", helpers.PushToPipe(pipe, false, address))
	time.Sleep(time.Second)

	out := &jobState.State{}
	t.Run("Stats", helpers.Stats(address, out))

	assert.Equal(t, pipe, out.Pipeline)
	assert.Equal(t, "sqs", out.Driver)
	assert.Equal(t, fmt.Sprintf("%s/%s/%s", os.Getenv("RR_SQS_TEST_ENDPOINT"), os.Getenv("RR_SQS_TEST_ACCOUNT_ID"), queue), out.Queue)

	time.Sleep(time.Second)
	t.Run("ResumePipeline", helpers.ResumePipes(address, pipe))
	time.Sleep(time.Second * 7)

	out = &jobState.State{}
	t.Run("Stats", helpers.Stats(address, out))

	assert.Equal(t, pipe, out.Pipeline)
	assert.Equal(t, "sqs", out.Driver)
	assert.Equal(t, fmt.Sprintf("%s/%s/%s", os.Getenv("RR_SQS_TEST_ENDPOINT"), os.Getenv("RR_SQS_TEST_ACCOUNT_ID"), queue), out.Queue)

	t.Run("DestroyPipeline", helpers.DestroyPipelines(address, pipe))

	stopCh <- struct{}{}
	wg.Wait()
}

func TestSQSRawPayload(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-sqs-raw.yaml",
	}

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "sqs-raw-payload")
	})

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		l,
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqsPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	if err != nil {
		t.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}
	wg.Add(1)

	stopCh := make(chan struct{}, 1)

	go func() {
		defer wg.Done()
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	}()

	time.Sleep(time.Second * 3)

	awsConf, err := sqsConf.LoadDefaultConfig(context.Background(),
		sqsConf.WithBaseEndpoint(os.Getenv("RR_SQS_TEST_ENDPOINT")),
		sqsConf.WithRegion(os.Getenv("RR_SQS_TEST_REGION")),
		sqsConf.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(os.Getenv("RR_SQS_TEST_KEY"),
			os.Getenv("RR_SQS_TEST_SECRET"), "")))
	require.NoError(t, err)

	// config with retries
	client := sqs.NewFromConfig(awsConf, func(o *sqs.Options) {
		o.Retryer = retry.NewStandard(func(so *retry.StandardOptions) {
			so.MaxAttempts = 60
		})
	})

	queueURL, err := getQueueURL(client, "sqs-raw-payload")
	require.NoError(t, err)

	body := "fooobooobzzzzaaaaafdsasdfas"

	attr := make(map[string]types.MessageAttributeValue)
	attr["foo"] = types.MessageAttributeValue{DataType: aws.String("String"), BinaryValue: nil, BinaryListValues: nil,
		StringListValues: nil, StringValue: aws.String("fooooooobaaaaarrrrr")}

	_, err = client.SendMessage(context.Background(), &sqs.SendMessageInput{
		MessageBody:       &body,
		QueueUrl:          queueURL,
		DelaySeconds:      0,
		MessageAttributes: attr,
	})
	require.NoError(t, err)

	time.Sleep(time.Second * 10)
	t.Run("DestroyPipeline", helpers.DestroyPipelines("127.0.0.1:6001", "test-raw"))

	stopCh <- struct{}{}
	wg.Wait()

	assert.Equal(t, 1, oLogger.FilterMessageSnippet("job was processed successfully").Len())
}

func TestSQSOTEL(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.1.0",
		Path:    "configs/.rr-sqs-otel.yaml",
	}

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "sqs-otel")
	})

	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&otel.Plugin{},
		&logger.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqsPlugin.Plugin{},
	)
	assert.NoError(t, err)

	err = cont.Init()
	if err != nil {
		t.Fatal(err)
	}

	ch, err := cont.Serve()
	if err != nil {
		t.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	wg := &sync.WaitGroup{}
	wg.Add(1)

	stopCh := make(chan struct{}, 1)

	go func() {
		defer wg.Done()
		for {
			select {
			case e := <-ch:
				assert.Fail(t, "error", e.Error.Error())
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
			case <-sig:
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			case <-stopCh:
				// timeout
				err = cont.Stop()
				if err != nil {
					assert.FailNow(t, "error", err.Error())
				}
				return
			}
		}
	}()

	time.Sleep(time.Second * 3)
	t.Run("PushPipeline", helpers.PushToPipe("test-1", false, "127.0.0.1:7766"))
	time.Sleep(time.Second * 2)

	t.Run("DestroyPipeline", helpers.DestroyPipelines("127.0.0.1:7766", "test-1"))
	time.Sleep(time.Second)

	stopCh <- struct{}{}
	wg.Wait()

	resp, err := http.Get("http://127.0.0.1:9411/api/v2/spans?serviceName=rr_test_sqs") //nolint:noctx
	assert.NoError(t, err)

	buf, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)

	var spans []string
	err = json.Unmarshal(buf, &spans)
	assert.NoError(t, err)

	sort.Slice(spans, func(i, j int) bool {
		return spans[i] < spans[j]
	})

	expected := []string{
		"destroy_pipeline",
		"jobs_listener",
		"push",
		"sqs_listener",
		"sqs_push",
		"sqs_stop",
	}
	assert.Equal(t, expected, spans)

	t.Cleanup(func() {
		_ = resp.Body.Close()
	})
}

func getQueueURL(client *sqs.Client, queueName string) (*string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()
	out, err := client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: &queueName})
	if err != nil {
		return nil, err
	}

	return out.QueueUrl, nil
}

func declareSQSPipe(queue string, address string, pipeline string) func(t *testing.T) {
	return func(t *testing.T) {
		conn, err := net.Dial("tcp", address)
		assert.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		pipe := &jobsProto.DeclareRequest{Pipeline: map[string]string{
			"driver":             "sqs",
			"name":               pipeline,
			"queue":              queue,
			"prefetch":           "10",
			"priority":           "3",
			"visibility_timeout": "0",
			"wait_time_seconds":  "3",
			"tags":               `{"key":"value"}`,
		}}

		er := &jobsProto.Empty{}
		err = client.Call("jobs.Declare", pipe, er)
		assert.NoError(t, err)
	}
}

func declareSQSPipeFifo(queue, address string) func(t *testing.T) {
	return func(t *testing.T) {
		conn, err := net.Dial("tcp", address)
		assert.NoError(t, err)
		client := rpc.NewClientWithCodec(goridgeRpc.NewClientCodec(conn))

		pipe := &jobsProto.DeclareRequest{Pipeline: map[string]string{
			"driver":             "sqs",
			"name":               "test-3",
			"queue":              queue,
			"prefetch":           "10",
			"priority":           "3",
			"visibility_timeout": "0",
			"message_group_id":   "RR",
			"wait_time_seconds":  "3",
			"attributes":         `{"FifoQueue":"true"}`,
			"tags":               `{"key":"value"}`,
		}}

		er := &jobsProto.Empty{}
		err = client.Call("jobs.Declare", pipe, er)
		assert.NoError(t, err)
	}
}
