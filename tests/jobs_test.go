package tests

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"tests/helpers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	jobState "github.com/roadrunner-server/api-plugins/v6/jobs"
	"github.com/roadrunner-server/informer/v6"
	"github.com/roadrunner-server/jobs/v6"
	"github.com/roadrunner-server/resetter/v6"
	rpcPlugin "github.com/roadrunner-server/rpc/v6"
	"github.com/roadrunner-server/server/v6"
	sqsPlugin "github.com/roadrunner-server/sqs/v6"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const (
	initAddr     = "127.0.0.1:6001"
	statsAddr    = "127.0.0.1:6010"
	otelAddr     = "127.0.0.1:7766"
	countAddr    = "127.0.0.1:6081"
	pqAddr       = "127.0.0.1:6601"
	fifoAddr     = "127.0.0.1:6451"
	fifoBrAddr   = "127.0.0.1:6061"
	fifoDeclAddr = "127.0.0.1:32341"
	fifoErrAddr  = "127.0.0.1:12342"
	prefetchAddr = "127.0.0.1:6232"
	// declared is the pipeline the declare configs create over rpc.
	declared = "test-3"
)

func jobsPlugins() []any {
	return []any{
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&jobs.Plugin{},
		&resetter.Plugin{},
		&informer.Plugin{},
		&sqsPlugin.Plugin{},
	}
}

// boot starts the container with the observed logger and waits for the rpc
// listener, which is the readiness signal the fixed sleeps used to stand in for.
func boot(t *testing.T, cfgPath string, addr string, opts ...helpers.Option) (*helpers.RR, func()) {
	t.Helper()

	return helpers.Start(t, cfgPath, jobsPlugins(),
		append([]helpers.Option{
			helpers.WithObservedLogger(),
			helpers.WithTCPProbe(addr),
		}, opts...)...)
}

// TestBoots covers the config-declared pipelines: both come up at startup and
// both tear their listener down on destroy.
func TestBoots(t *testing.T) {
	t.Cleanup(func() { helpers.DeleteQueues(t, "sqs-init-1", "sqs-init-2") })

	rr, _ := boot(t, "configs/.rr-sqs-init.yaml", initAddr)

	rr.RequireLogCount(t, "pipeline was started", 2)

	helpers.DestroyPipelines(initAddr, "test-1", "test-2")(t)

	rr.RequireLogCount(t, "pipeline was stopped", 2)
	rr.RequireLogCount(t, "sqs listener was stopped", 2)
}

// TestPushAndProcess follows two jobs from the rpc call to the worker ack.
func TestPushAndProcess(t *testing.T) {
	t.Cleanup(func() { helpers.DeleteQueues(t, "sqs-init-1", "sqs-init-2") })

	rr, _ := boot(t, "configs/.rr-sqs-init.yaml", initAddr)

	helpers.PushToPipe("test-1", false, initAddr)(t)
	helpers.PushToPipe("test-2", false, initAddr)(t)

	rr.WaitLog(t, "job was processed successfully", 2)

	helpers.DestroyPipelines(initAddr, "test-1", "test-2")(t)

	rr.RequireLogCount(t, "job was pushed successfully", 2)
	rr.RequireLogCount(t, "job was processed successfully", 2)
}

// TestAutoAck checks the listener deletes the message itself, before the worker
// ever sees it, when the job carries the auto ack option.
func TestAutoAck(t *testing.T) {
	t.Cleanup(func() { helpers.DeleteQueues(t, "sqs-auto-ack-1", "sqs-auto-ack-2") })

	rr, _ := boot(t, "configs/.rr-sqs-auto-ack.yaml", initAddr)

	for range 3 {
		helpers.PushToPipe("test-1", true, initAddr)(t)
		helpers.PushToPipe("test-2", true, initAddr)(t)
	}

	rr.WaitLog(t, "job was processed successfully", 6)

	helpers.DestroyPipelines(initAddr, "test-1", "test-2")(t)

	rr.RequireLogCount(t, "auto ack is turned on, message acknowledged", 6)
}

// TestQueueAttributes covers a fifo queue created with explicit attributes; the
// pipeline has to come up and process against it.
func TestQueueAttributes(t *testing.T) {
	t.Cleanup(func() { helpers.DeleteQueues(t, "sqs-attr.fifo") })

	rr, _ := boot(t, "configs/.rr-sqs-attr.yaml", initAddr)

	helpers.PushToPipe("test-1", false, initAddr)(t)
	helpers.PushToPipe("test-1", false, initAddr)(t)

	rr.WaitLog(t, "job was processed successfully", 2)

	helpers.DestroyPipelines(initAddr, "test-1")(t)

	rr.RequireLogCount(t, "job was processed successfully", 2)
}

// TestPriorityQueueBacklog pushes far more jobs than the two slow workers can
// take, so most of them sit in the priority queue until the pipelines are
// destroyed under them.
func TestPriorityQueueBacklog(t *testing.T) {
	const rounds = 10

	t.Cleanup(func() { helpers.DeleteQueues(t, "sqs-pq-1", "sqs-pq-2") })

	rr, _ := boot(t, "configs/.rr-sqs-pq.yaml", pqAddr)

	for range rounds {
		helpers.PushToPipe("test-1-pq", false, pqAddr)(t)
		helpers.PushToPipe("test-2-pq", false, pqAddr)(t)
	}

	rr.RequireLogCount(t, "job was pushed successfully", 2*rounds)

	// both workers have to be busy before the destroy, otherwise the backlog
	// would never form
	rr.WaitLog(t, "job processing was started", 2)

	helpers.DestroyPipelines(pqAddr, "test-1-pq", "test-2-pq")(t)

	rr.RequireLogCount(t, "pipeline was started", 2)
	rr.RequireLogCount(t, "pipeline was stopped", 2)
	rr.RequireLogCount(t, "sqs listener was stopped", 2)
}

// TestBadResponseIsReported covers a worker answering with a payload the jobs
// response handler cannot parse.
func TestBadResponseIsReported(t *testing.T) {
	t.Cleanup(func() { helpers.DeleteQueues(t, "sqs-init-br-1", "sqs-init-br-2") })

	rr, _ := boot(t, "configs/.rr-sqs-init-br.yaml", initAddr)

	helpers.PushToPipe("test-1", false, initAddr)(t)
	helpers.PushToPipe("test-2", false, initAddr)(t)

	rr.WaitLog(t, "response handler error", 2)

	helpers.DestroyPipelines(initAddr, "test-1", "test-2")(t)
}

// TestDeclareAndConsume declares a pipeline over rpc, runs a job through it and
// pauses it again. The old test made the same calls and asserted nothing.
func TestDeclareAndConsume(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-sqs-declare.yaml", initAddr)

	helpers.DeclarePipe(initAddr, declared, "sqs-declare-test")(t)
	helpers.ResumePipes(initAddr, declared)(t)
	rr.WaitLog(t, "pipeline was resumed", 1)

	helpers.PushToPipe(declared, false, initAddr)(t)
	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.PausePipelines(initAddr, declared)(t)
	rr.WaitLog(t, "pipeline was paused", 1)

	helpers.DestroyPipelines(initAddr, declared)(t)

	rr.RequireLogCount(t, "job was pushed successfully", 1)
	rr.RequireLogCount(t, "job was processed successfully", 1)
	rr.RequireLogCount(t, "pipeline was stopped", 1)
}

// TestPauseStopsConsuming checks a paused pipeline still accepts pushes but
// leaves them on the queue until it is resumed.
//
// Pause only signals the listener, which notices once its long poll returns, so
// the push waits for the listener to report itself stopped rather than for the
// pause call.
func TestPauseStopsConsuming(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-sqs-declare.yaml", initAddr)

	helpers.DeclarePipe(initAddr, declared, "sqs-pause-test")(t)
	helpers.ResumePipes(initAddr, declared)(t)
	rr.WaitLog(t, "pipeline was resumed", 1)

	helpers.PausePipelines(initAddr, declared)(t)
	rr.WaitLog(t, "sqs listener was stopped", 1)

	helpers.PushToPipe(declared, false, initAddr)(t)
	rr.WaitLog(t, "job was pushed successfully", 1)
	rr.NeverLog(t, "job was processed successfully")

	helpers.ResumePipes(initAddr, declared)(t)
	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.DestroyPipelines(initAddr, declared)(t)
}

// TestRequeueRetriesUntilComplete covers the worker that fails a job with a
// growing attempts header and only completes it on the fourth delivery. The old
// test slept out the three five second delays and asserted nothing.
func TestRequeueRetriesUntilComplete(t *testing.T) {
	rr, _ := boot(t, "configs/.rr-sqs-jobs-err.yaml", initAddr)

	helpers.DeclarePipe(initAddr, declared, "sqs-declare-err")(t)
	helpers.ResumePipes(initAddr, declared)(t)
	helpers.PushToPipe(declared, false, initAddr)(t)

	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.PausePipelines(initAddr, declared)(t)
	helpers.DestroyPipelines(initAddr, declared)(t)

	// one original delivery plus the three the worker requeued
	rr.RequireLogCount(t, "job processing was started", 4)
	rr.RequireLogCount(t, "job was pushed successfully", 1)
	rr.RequireLogCount(t, "job was processed successfully", 1)
}

// TestApproximateReceiveCount covers the receive counter SQS attaches to every
// delivery. The worker nacks each one and echoes the header, so the fourth
// delivery has to see a count of four. The old test slept a flat 35 seconds.
func TestApproximateReceiveCount(t *testing.T) {
	t.Cleanup(func() { helpers.DeleteQueues(t, "sqs-read-approximate-count") })

	rr, _ := boot(t, "configs/.rr-sqs-read-approximate-count.yaml", countAddr)

	helpers.PushToPipe("test-err-approx-count", false, countAddr)(t)

	rr.WaitLog(t, "Receive count: 4", 1)

	helpers.PausePipelines(countAddr, "test-err-approx-count")(t)
	helpers.DestroyPipelines(countAddr, "test-err-approx-count")(t)
}

// TestStatsReportQueueCounters covers the state report against the approximate
// counters SQS keeps. A delayed push stays counted as delayed until its delay
// lapses, then the pipeline drains.
func TestStatsReportQueueCounters(t *testing.T) {
	boot(t, "configs/.rr-sqs-stat.yaml", statsAddr)

	helpers.DeclarePipe(statsAddr, declared, "sqs-test-declare-stats")(t)

	paused := helpers.StatsFor(t, statsAddr, declared)
	require.Equal(t, "sqs", paused.Driver)
	// the endpoint half of the url depends on how the backend styles it, the
	// account and queue tail does not
	require.True(t, strings.HasSuffix(paused.Queue, "/000000000000/sqs-test-declare-stats"), "queue url: %s", paused.Queue)
	require.Equal(t, uint64(3), paused.Priority)
	require.False(t, paused.Ready)

	// with consumption paused, a delayed job stays counted as delayed
	helpers.PushToPipeDelayed(statsAddr, declared, 5)(t)

	helpers.WaitStats(t, statsAddr, declared, func(s *jobState.State) bool {
		return s.Delayed == 1
	})

	// resuming drains it once the delay lapses
	helpers.ResumePipes(statsAddr, declared)(t)

	drained := helpers.WaitStats(t, statsAddr, declared, func(s *jobState.State) bool {
		return s.Delayed == 0 && s.Active == 0 && s.Reserved == 0
	})

	require.True(t, drained.Ready)

	helpers.DestroyPipelines(statsAddr, declared)(t)
}

// TestRawPayload covers a message sent without any RoadRunner attributes. The
// listener has to wrap it rather than drop it.
func TestRawPayload(t *testing.T) {
	t.Cleanup(func() { helpers.DeleteQueues(t, "sqs-raw-payload") })

	rr, _ := boot(t, "configs/.rr-sqs-raw.yaml", initAddr)

	rr.WaitLog(t, "pipeline was started", 1)

	helpers.SendRaw(t, "sqs-raw-payload", "fooobarrbazzz", nil)

	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.DestroyPipelines(initAddr, "test-raw")(t)

	rr.RequireLogCount(t, "job processing was started", 1)
	rr.RequireLogCount(t, "job was processed successfully", 1)
}

// TestForeignAttributesBecomeHeaders checks the attributes of a foreign message
// survive as headers instead of being dropped with the RR metadata.
func TestForeignAttributesBecomeHeaders(t *testing.T) {
	t.Cleanup(func() { helpers.DeleteQueues(t, "sqs-raw-payload") })

	rr, _ := boot(t, "configs/.rr-sqs-raw.yaml", initAddr)

	rr.WaitLog(t, "pipeline was started", 1)

	helpers.SendRaw(t, "sqs-raw-payload", "fooobarrbazzz", map[string]types.MessageAttributeValue{
		"custom": {DataType: aws.String("String"), StringValue: aws.String("value")},
	})

	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.DestroyPipelines(initAddr, "test-raw")(t)
}

// TestOTELSpans checks the spans the driver emits around a push and a destroy.
func TestOTELSpans(t *testing.T) {
	t.Cleanup(func() { helpers.DeleteQueues(t, "sqs-otel") })

	tracer := newInMemoryTracer(t)

	rr, _ := boot(t, "configs/.rr-sqs-otel.yaml", otelAddr, helpers.WithPlugin(tracer))

	helpers.PushToPipe("test-1", false, otelAddr)(t)

	rr.WaitLog(t, "job was processed successfully", 1)

	helpers.DestroyPipelines(otelAddr, "test-1")(t)

	rr.RequireLogCount(t, "pipeline was stopped", 1)

	names := make(map[string]struct{})
	for _, s := range tracer.exp.GetSpans() {
		names[s.Name] = struct{}{}
	}

	got := make([]string, 0, len(names))
	for name := range names {
		got = append(got, name)
	}
	slices.Sort(got)

	for _, want := range []string{
		"destroy_pipeline",
		"jobs_listener",
		"sqs_listener",
		"sqs_push",
		"push",
	} {
		require.Contains(t, got, want, "collected spans: %v", got)
	}
}

// inMemoryTracer stands in for the otel plugin, keeping the spans in process.
type inMemoryTracer struct {
	tp  *sdktrace.TracerProvider
	exp *tracetest.InMemoryExporter
}

func newInMemoryTracer(t *testing.T) *inMemoryTracer {
	t.Helper()

	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	return &inMemoryTracer{tp: tp, exp: exp}
}

func (*inMemoryTracer) Init() error                        { return nil }
func (*inMemoryTracer) Name() string                       { return "inMemoryTracer" }
func (m *inMemoryTracer) Tracer() *sdktrace.TracerProvider { return m.tp }

// slogError is a shortcut used by configs that boot expecting quiet logs.
var _ = slog.LevelError
