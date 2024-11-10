package sqs

import (
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	mocklogger "tests/mock"

	toxiproxy "github.com/Shopify/toxiproxy/v2/client"
	"github.com/roadrunner-server/config/v5"
	"github.com/roadrunner-server/endure/v2"
	"github.com/roadrunner-server/informer/v5"
	"github.com/roadrunner-server/jobs/v5"
	"github.com/roadrunner-server/resetter/v5"
	"github.com/roadrunner-server/server/v5"
	"github.com/roadrunner-server/sqs/v5"
	"go.uber.org/zap"

	"tests/helpers"

	rpcPlugin "github.com/roadrunner-server/rpc/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDurabilitySQS(t *testing.T) {
	newClient := toxiproxy.NewClient("127.0.0.1:8474")

	_, err := newClient.CreateProxy("redial", "127.0.0.1:19324", "127.0.0.1:4566")
	require.NoError(t, err)
	defer helpers.DeleteProxy("redial", t)

	cont := endure.New(slog.LevelDebug)

	cfg := &config.Plugin{
		Version: "2023.3.0",
		Path:    "configs/.rr-sqs-durability-redial.yaml",
	}

	l, oLogger := mocklogger.ZapTestLogger(zap.DebugLevel)
	err = cont.RegisterAll(
		l,
		cfg,
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqs.Plugin{},
	)
	require.NoError(t, err)

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
	helpers.DisableProxy("redial", t)

	go func() {
		time.Sleep(time.Second)
		t.Run("PushPipelineWhileRedialing-1", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
		t.Run("PushPipelineWhileRedialing-2", helpers.PushToPipe("test-2", false, "127.0.0.1:6001"))
	}()

	time.Sleep(time.Second * 5)
	helpers.EnableProxy("redial", t)
	time.Sleep(time.Second * 5)

	t.Run("PushPipelineWhileRedialing-3", helpers.PushToPipe("test-1", false, "127.0.0.1:6001"))
	t.Run("PushPipelineWhileRedialing-4", helpers.PushToPipe("test-2", false, "127.0.0.1:6001"))

	time.Sleep(time.Second * 10)

	helpers.DestroyPipelines("127.0.0.1:6001", "test-1", "test-2")

	stopCh <- struct{}{}
	wg.Wait()

	assert.Equal(t, 4, oLogger.FilterMessageSnippet("job was pushed successfully").Len(), "Exactly 4 jobs must have been pushed.")
	assert.GreaterOrEqual(t, oLogger.FilterMessageSnippet("job was processed successfully").Len(), 4, "4 or more jobs must have been processed.")
}
