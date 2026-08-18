package sqsjobs

import (
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/roadrunner-server/api-plugins/v6/jobs"
	"github.com/stretchr/testify/require"
)

// testPipeline is the jobs.Pipeline the jobs plugin hands to the driver.
type testPipeline struct {
	name     string
	priority int64
}

func (p *testPipeline) Name() string                      { return p.name }
func (*testPipeline) Driver() string                      { return pluginName }
func (p *testPipeline) Priority() int64                   { return p.priority }
func (*testPipeline) With(string, any)                    {}
func (*testPipeline) Has(string) bool                     { return false }
func (*testPipeline) String(_ string, d string) string    { return d }
func (*testPipeline) Int(_ string, d int) int             { return d }
func (*testPipeline) Bool(_ string, d bool) bool          { return d }
func (*testPipeline) Map(string, map[string]string) error { return nil }
func (*testPipeline) Get(string) any                      { return nil }

var _ jobs.Pipeline = (*testPipeline)(nil)

// newTestDriver builds a driver with everything unpack touches and nothing
// else, so the message decoding can be covered without an SQS endpoint.
func newTestDriver() *Driver {
	d := &Driver{log: slog.New(slog.DiscardHandler), queue: aws.String("sqs-test")}

	var pipe jobs.Pipeline = &testPipeline{name: "test-1", priority: 11}
	d.pipeline.Store(&pipe)

	return d
}

func strAttr(v string) types.MessageAttributeValue {
	return types.MessageAttributeValue{DataType: aws.String("String"), StringValue: aws.String(v)}
}

// TestUnpackRoundTrip covers a message this driver produced, with the metadata
// riding in the RR message attributes.
func TestUnpackRoundTrip(t *testing.T) {
	headers, err := json.Marshal(map[string][]string{"test": {"test2"}})
	require.NoError(t, err)

	item := newTestDriver().unpack(&types.Message{
		Body: aws.String(`{"hello":"world"}`),
		MessageAttributes: map[string]types.MessageAttributeValue{
			jobs.RRJob:      strAttr("some/php/namespace"),
			jobs.RRID:       strAttr("job-id"),
			jobs.RRDelay:    strAttr("5"),
			jobs.RRPriority: strAttr("3"),
			jobs.RRAutoAck:  strAttr("true"),
			jobs.RRHeaders:  {DataType: aws.String("Binary"), BinaryValue: headers},
		},
	})

	require.Equal(t, "job-id", item.ID())
	require.Equal(t, "some/php/namespace", item.Job)
	require.Equal(t, int64(3), item.Priority())
	require.Equal(t, "test-1", item.GroupID())
	require.Equal(t, []byte(`{"hello":"world"}`), item.Body())
	require.Equal(t, map[string][]string{"test": {"test2"}}, item.Headers())
	require.Equal(t, 5, item.Options.Delay)
	require.True(t, item.Options.AutoAck)
}

// TestUnpackForeignMessage covers a message something other than RoadRunner put
// on the queue: no RR attributes at all. The item is synthesized and the
// foreign attributes become headers.
func TestUnpackForeignMessage(t *testing.T) {
	item := newTestDriver().unpack(&types.Message{
		Body: aws.String("fooobarrbazzz"),
		MessageAttributes: map[string]types.MessageAttributeValue{
			"custom": strAttr("value"),
		},
		Attributes: map[string]string{"ApproximateReceiveCount": "2"},
	})

	require.Equal(t, auto, item.Job)
	require.NotEmpty(t, item.ID())
	require.Equal(t, []byte("fooobarrbazzz"), item.Body())
	require.Equal(t, "test-1", item.GroupID())
	require.Equal(t, []string{"value"}, item.Headers()["custom"])
}

// TestUnpackMalformedAttributesFallBack covers values RoadRunner would never
// write: the delay is dropped and the priority falls back to the pipeline's.
func TestUnpackMalformedAttributesFallBack(t *testing.T) {
	item := newTestDriver().unpack(&types.Message{
		Body: aws.String("{}"),
		MessageAttributes: map[string]types.MessageAttributeValue{
			jobs.RRID:       strAttr("job-id"),
			jobs.RRJob:      strAttr("some/php/namespace"),
			jobs.RRDelay:    strAttr("soon"),
			jobs.RRPriority: strAttr("high"),
			jobs.RRHeaders:  {DataType: aws.String("Binary"), BinaryValue: []byte("not-json")},
		},
	})

	require.Zero(t, item.Options.Delay)
	require.Equal(t, int64(11), item.Priority())
}

func TestItemContext(t *testing.T) {
	item := &Item{
		Job:     "some/php/namespace",
		Ident:   "job-id",
		headers: map[string][]string{"test": {"test2"}},
		Options: &Options{Pipeline: "test-1", Queue: "sqs-test"},
	}

	data, err := item.Context()
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, "job-id", got["id"])
	require.Equal(t, "sqs", got["driver"])
	require.Equal(t, "sqs-test", got["queue"])
	require.Equal(t, "test-1", got["pipeline"])
}

func TestDelayDuration(t *testing.T) {
	require.Equal(t, 5*time.Second, (&Options{Delay: 5}).DelayDuration())
	require.Zero(t, (&Options{}).DelayDuration())
}

// newStoppedItem returns an item whose pipeline has already been stopped.
func newStoppedItem() *Item {
	stopped := &atomic.Uint64{}
	stopped.Store(1)

	return &Item{Options: &Options{stopped: stopped}}
}

// TestStoppedPipelineRejectsReply covers the guard that keeps a late worker
// reply from touching a queue the driver has already left.
func TestStoppedPipelineRejectsReply(t *testing.T) {
	require.ErrorContains(t, newStoppedItem().Ack(), "pipeline is probably stopped")
	require.ErrorContains(t, newStoppedItem().Nack(), "pipeline is probably stopped")
	require.ErrorContains(t, newStoppedItem().NackWithOptions(true, 0), "pipeline is probably stopped")
	require.ErrorContains(t, newStoppedItem().Requeue(nil, 0), "pipeline is probably stopped")
}

// TestAutoAckSkipsBroker checks the worker reply is a no-op once the listener
// already deleted the message, so none of these reach the nil client. The
// in-flight bookkeeping still runs, releasing the prefetch slot.
func TestAutoAckSkipsBroker(t *testing.T) {
	inFlight := &atomic.Int64{}
	inFlight.Store(2)

	newItem := func() *Item {
		return &Item{Options: &Options{
			AutoAck:     true,
			stopped:     &atomic.Uint64{},
			cond:        sync.NewCond(&sync.Mutex{}),
			msgInFlight: inFlight,
		}}
	}

	require.NoError(t, newItem().Ack())
	require.NoError(t, newItem().NackWithOptions(false, 0))
	require.Equal(t, int64(0), inFlight.Load(), "each reply releases one in-flight slot")
}

func TestConvAttr(t *testing.T) {
	require.Equal(t,
		map[string][]string{"a": {"1"}, "b": {"2"}},
		convAttr(map[string]string{"a": "1", "b": "2"}))
}

// TestConvMessageAttrSkipsRRKeys checks the RoadRunner-owned attribute keys do
// not leak into the user headers of a foreign message.
func TestConvMessageAttrSkipsRRKeys(t *testing.T) {
	h := map[string][]string{}
	convMessageAttr(map[string]types.MessageAttributeValue{
		jobs.RRID:  strAttr("id"),
		jobs.RRJob: strAttr("job"),
		"custom":   strAttr("value"),
		"binary":   {DataType: aws.String("Binary"), BinaryValue: []byte("bin")},
	}, &h)

	require.NotContains(t, h, jobs.RRID)
	require.NotContains(t, h, jobs.RRJob)
	require.Equal(t, []string{"value"}, h["custom"])
	require.Equal(t, []string{"bin"}, h["binary"])
}

// TestPackFifoDedup covers the deduplication id on a fifo queue: a plain push
// reuses the job id so a double push stays idempotent, while a requeued copy
// has to differ from the original or SQS silently drops it inside the five
// minute dedup window and the job is lost.
func TestPackFifoDedup(t *testing.T) {
	fifo := aws.String("sqs-test.fifo")
	newFifoItem := func() *Item {
		return &Item{Ident: "job-id", Payload: []byte("{}"), Options: &Options{}}
	}

	pushed, err := newFifoItem().pack(fifo, fifo, "RR")
	require.NoError(t, err)
	require.Equal(t, "job-id", aws.ToString(pushed.MessageDeduplicationId))

	item := newFifoItem()
	item.Options.requeued = true
	requeued, err := item.pack(fifo, fifo, "RR")
	require.NoError(t, err)
	require.NotEqual(t, "job-id", aws.ToString(requeued.MessageDeduplicationId))
	require.NotEmpty(t, aws.ToString(requeued.MessageDeduplicationId))

	// standard queues carry no deduplication id at all
	std := aws.String("sqs-test")
	plain, err := newFifoItem().pack(std, std, "")
	require.NoError(t, err)
	require.Nil(t, plain.MessageDeduplicationId)
}

// TestPackFifoStripsDelay covers the per-message delay, which SQS only allows
// on standard queues.
func TestPackFifoStripsDelay(t *testing.T) {
	fifo := aws.String("sqs-test.fifo")
	item := &Item{Ident: "job-id", Payload: []byte("{}"), Options: &Options{Delay: 5}}

	out, err := item.pack(fifo, fifo, "RR")
	require.NoError(t, err)
	require.Zero(t, out.DelaySeconds)

	std := aws.String("sqs-test")
	out, err = item.pack(std, std, "")
	require.NoError(t, err)
	require.Equal(t, int32(5), out.DelaySeconds)
}
