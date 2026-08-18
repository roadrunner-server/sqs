package tests

import (
	"testing"

	"tests/helpers"
)

// fifoDeclare declares a fifo pipeline over rpc. SQS requires the .fifo suffix
// and the FifoQueue attribute together, and every push needs a message group.
func fifoDeclare(t *testing.T, address string, queue string) {
	t.Helper()

	require := helpers.Declare(t, address, map[string]string{
		"driver":             "sqs",
		"name":               declared,
		"queue":              queue,
		"prefetch":           "10",
		"priority":           "3",
		"visibility_timeout": "0",
		"message_group_id":   "RR",
		"wait_time_seconds":  "3",
		"attributes":         `{"FifoQueue":"true"}`,
		"tags":               `{"key":"value"}`,
	})
	if require != nil {
		t.Fatalf("declare fifo pipeline: %v", require)
	}

	t.Cleanup(func() { helpers.DeleteQueues(t, queue) })
}

// TestFifoPushAndProcess follows two jobs through the config-declared fifo
// pipelines, whose message ordering constraints run a stricter broker path.
func TestFifoPushAndProcess(t *testing.T) {
	t.Cleanup(func() { helpers.DeleteQueues(t, "sqs-init-1.fifo", "sqs-init-2.fifo") })

	rr, _ := boot(t, "configs/.rr-sqs-init_fifo.yaml", fifoAddr)

	helpers.PushToPipe("test-1", false, fifoAddr)(t)
	helpers.PushToPipe("test-2", false, fifoAddr)(t)

	rr.WaitLog(t, "job was processed successfully", 2)

	helpers.DestroyPipelines(fifoAddr, "test-1", "test-2")(t)

	rr.RequireLogCount(t, "job was pushed successfully", 2)
	rr.RequireLogCount(t, "job was processed successfully", 2)
	rr.RequireLogCount(t, "sqs listener was stopped", 2)
}

// TestFifoAutoAck checks the auto ack path against a fifo queue.
func TestFifoAutoAck(t *testing.T) {
	t.Cleanup(func() { helpers.DeleteQueues(t, "sqs-init-1-auto-ack.fifo", "sqs-init-2-auto-ack.fifo") })

	rr, _ := boot(t, "configs/.rr-sqs-init_fifo_auto_ack.yaml", fifoAddr)

	helpers.PushToPipe("test-1", true, fifoAddr)(t)
	helpers.PushToPipe("test-2", true, fifoAddr)(t)

	rr.WaitLog(t, "job was processed successfully", 2)

	helpers.DestroyPipelines(fifoAddr, "test-1", "test-2")(t)

	rr.RequireLogCount(t, "auto ack is turned on, message acknowledged", 2)
}

// TestFifoBadResponseIsReported covers the response handler error path against
// a fifo queue.
func TestFifoBadResponseIsReported(t *testing.T) {
	t.Cleanup(func() { helpers.DeleteQueues(t, "sqs-init-br-1.fifo", "sqs-init-br-2.fifo") })

	rr, _ := boot(t, "configs/.rr-sqs-init-br_fifo.yaml", fifoBrAddr)

	helpers.PushToPipe("test-1", false, fifoBrAddr)(t)
	helpers.PushToPipe("test-2", false, fifoBrAddr)(t)

	rr.WaitLog(t, "response handler error", 2)

	helpers.DestroyPipelines(fifoBrAddr, "test-1", "test-2")(t)
}

// TestFifoDeclareAndConsume declares a fifo pipeline over rpc and runs a job
// through it. The old test made the same calls and asserted nothing.
func TestFifoDeclareAndConsume(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-sqs-declare_fifo.yaml", fifoDeclAddr)

	fifoDeclare(t, fifoDeclAddr, "sqs-default-decl.fifo")
	helpers.ResumePipes(fifoDeclAddr, declared)(t)
	rr.WaitLog(t, "pipeline was resumed", 1)

	helpers.PushToPipe(declared, false, fifoDeclAddr)(t)
	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.PausePipelines(fifoDeclAddr, declared)(t)
	rr.WaitLog(t, "pipeline was paused", 1)

	helpers.DestroyPipelines(fifoDeclAddr, declared)(t)

	rr.RequireLogCount(t, "job was processed successfully", 1)
	rr.RequireLogCount(t, "pipeline was stopped", 1)
}

// TestFifoRequeueRetriesUntilComplete runs the growing attempts header worker
// against a fifo queue.
func TestFifoRequeueRetriesUntilComplete(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-sqs-jobs-err_fifo.yaml", fifoErrAddr)

	fifoDeclare(t, fifoErrAddr, "sqs-default-err.fifo")
	helpers.ResumePipes(fifoErrAddr, declared)(t)
	helpers.PushToPipe(declared, false, fifoErrAddr)(t)

	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.PausePipelines(fifoErrAddr, declared)(t)
	helpers.DestroyPipelines(fifoErrAddr, declared)(t)

	// one original delivery plus the three the worker requeued
	rr.RequireLogCount(t, "job processing was started", 4)
	rr.RequireLogCount(t, "job was processed successfully", 1)
}

// TestFifoPrefetchLimit pushes 30 jobs at two slow fifo pipelines whose
// prefetch is smaller than the backlog, so the listener has to hold messages
// back until in-flight ones finish. The old test waited out a flat 70 seconds.
func TestFifoPrefetchLimit(t *testing.T) {
	const rounds = 15

	t.Cleanup(func() { helpers.DeleteQueues(t, "sqs-init-prefetch-1.fifo", "sqs-init-prefetch-2.fifo") })

	rr, _ := boot(t, "configs/.rr-sqs-init_fifo-prefetch.yaml", prefetchAddr)

	for range rounds {
		helpers.PushToPipe("test-1", false, prefetchAddr)(t)
		helpers.PushToPipe("test-2", false, prefetchAddr)(t)
	}

	rr.RequireLogCount(t, "job was pushed successfully", 2*rounds)
	rr.WaitLog(t, "job was processed successfully", 2*rounds)

	helpers.DestroyPipelines(prefetchAddr, "test-1", "test-2")(t)

	rr.RequireLogCount(t, "job was processed successfully", 2*rounds)
}
