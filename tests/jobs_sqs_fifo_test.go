package sqs

import (
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	"tests/helpers"
	mocklogger "tests/mock"

	"github.com/roadrunner-server/config/v5"
	"github.com/roadrunner-server/endure/v2"
	"github.com/roadrunner-server/informer/v5"
	"github.com/roadrunner-server/jobs/v5"
	"github.com/roadrunner-server/logger/v5"
	"github.com/roadrunner-server/resetter/v5"
	rpcPlugin "github.com/roadrunner-server/rpc/v5"
	"github.com/roadrunner-server/server/v5"
	"github.com/roadrunner-server/sqs/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSQSInitFifo(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-sqs-init_fifo.yaml",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		l,
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqs.Plugin{},
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
	t.Run("PushPipelineFifo", helpers.PushToPipe("test-1", false, "127.0.0.1:6451"))
	t.Run("PushPipelineFifo", helpers.PushToPipe("test-2", false, "127.0.0.1:6451"))
	time.Sleep(time.Second * 2)

	t.Run("DestroyPipeline", helpers.DestroyPipelines("127.0.0.1:6451", "test-1", "test-2"))

	stopCh <- struct{}{}
	wg.Wait()
	time.Sleep(time.Second)

	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("receive message").Len(), 2)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("job was pushed successfully").Len(), 2)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("job was processed successfully").Len(), 2)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("sqs listener was stopped").Len(), 2)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("destroy signal received").Len(), 1)
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("pipeline was stopped").Len(), 2)

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "sqs-init-1.fifo", "sqs-init-2.fifo")
	})
}

func TestSQSInitFifoAutoAck(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-sqs-init_fifo_auto_ack.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		l,
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqs.Plugin{},
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
	t.Run("PushPipelineFifo", helpers.PushToPipe("test-1", true, "127.0.0.1:6451"))
	t.Run("PushPipelineFifo", helpers.PushToPipe("test-2", true, "127.0.0.1:6451"))
	time.Sleep(time.Second * 2)

	t.Run("DestroyPipeline", helpers.DestroyPipelines("127.0.0.1:6451", "test-1", "test-2"))

	stopCh <- struct{}{}
	wg.Wait()

	require.Equal(t, 2, oLogger.FilterMessageSnippet("auto ack is turned on, message acknowledged").Len())

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "sqs-init-1-auto-ack.fifo", "sqs-init-2-auto-ack.fifo")
	})
}

func TestSQSInitBadRespFifo(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Path:    "configs/.rr-sqs-init-br_fifo.yaml",
		Version: "2023.3.0",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err := cont.RegisterAll(
		l,
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqs.Plugin{},
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
	t.Run("PushPipelineFifo", helpers.PushToPipe("test-1", false, "127.0.0.1:6061"))
	t.Run("PushPipelineFifo", helpers.PushToPipe("test-2", false, "127.0.0.1:6061"))
	time.Sleep(time.Second)

	t.Run("DestroyPipeline", helpers.DestroyPipelines("127.0.0.1:6061", "test-1", "test-2"))

	stopCh <- struct{}{}
	wg.Wait()

	require.GreaterOrEqual(t, oLogger.FilterMessageSnippet("response handler error").Len(), 2)

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "sqs-init-br-1.fifo", "sqs-init-br-2.fifo")
	})
}

func TestSQSDeclareFifo(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-sqs-declare_fifo.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&logger.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqs.Plugin{},
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

	t.Run("DeclarePipelineFifo", declareSQSPipeFifo("sqs-default-decl.fifo", "127.0.0.1:32341"))
	t.Run("ConsumePipelineFifo", helpers.ResumePipes("127.0.0.1:32341", "test-3"))
	t.Run("PushPipelineFifo", helpers.PushToPipe("test-3", false, "127.0.0.1:32341"))
	time.Sleep(time.Second)
	t.Run("PausePipelineFifo", helpers.PausePipelines("127.0.0.1:32341", "test-3"))
	time.Sleep(time.Second)
	t.Run("DestroyPipelineFifo", helpers.DestroyPipelines("127.0.0.1:32341", "test-3"))

	time.Sleep(time.Second * 5)
	stopCh <- struct{}{}
	wg.Wait()

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "sqs-default-decl.fifo")
	})
}

func TestSQSJobsErrorFifo(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-sqs-jobs-err_fifo.yaml",
	}

	err := cont.RegisterAll(
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&logger.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqs.Plugin{},
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

	t.Run("DeclarePipelineFifo", declareSQSPipeFifo("sqs-default-err.fifo", "127.0.0.1:12342"))
	t.Run("ConsumePipelineFifo", helpers.ResumePipes("127.0.0.1:12342", "test-3"))
	t.Run("PushPipelineFifo", helpers.PushToPipe("test-3", false, "127.0.0.1:12342"))
	time.Sleep(time.Second * 25)
	t.Run("PausePipelineFifo", helpers.PausePipelines("127.0.0.1:12342", "test-3"))
	time.Sleep(time.Second)
	t.Run("DestroyPipelineFifo", helpers.DestroyPipelines("127.0.0.1:12342", "test-3"))

	time.Sleep(time.Second * 5)
	stopCh <- struct{}{}
	wg.Wait()

	time.Sleep(time.Second * 5)

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "sqs-default-err.fifo")
	})
}

func TestSQSPrefetch(t *testing.T) {
	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-sqs-init_fifo-prefetch.yaml",
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
		&sqs.Plugin{},
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
	for i := 0; i < 15; i++ {
		go func() {
			t.Run("PushPipelineFifo", helpers.PushToPipe("test-1", false, "127.0.0.1:6232"))
			t.Run("PushPipelineFifo", helpers.PushToPipe("test-2", false, "127.0.0.1:6232"))
		}()
	}

	time.Sleep(time.Second * 60)
	t.Run("DestroyPipeline", helpers.DestroyPipelines("127.0.0.1:6232", "test-1", "test-2"))
	stopCh <- struct{}{}

	wg.Wait()

	time.Sleep(time.Second * 10)

	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("prefetch limit was reached").Len(), 1, "1 queue must reach prefetch limit")
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("receive message").Len(), 30, "30 jobs must be received")
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("job was pushed successfully").Len(), 30, "30 jobs must be pushed")
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("job was processed successfully").Len(), 30, "30 jobs must be processed")
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("sqs listener was stopped").Len(), 2, "2 sqs listeners must be stopped")
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("destroy signal received").Len(), 1, "1 destroy signal must be received")
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("pipeline was stopped").Len(), 2, "2 pipelines must be stopped")

	t.Cleanup(func() {
		helpers.DeleteQueues(t, "sqs-init-prefetch-1.fifo", "sqs-init-prefetch-2.fifo")
	})
}
