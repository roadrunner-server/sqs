package sqsjobs

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/goccy/go-json"
	"github.com/google/uuid"
	"github.com/roadrunner-server/api/v4/plugins/v1/jobs"
	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/sdk/v4/utils"
	"go.uber.org/zap"
)

const (
	StringType              string = "String"
	NumberType              string = "Number"
	BinaryType              string = "Binary"
	ApproximateReceiveCount string = "ApproximateReceiveCount"
	fifoSuffix              string = ".fifo"
)

// RequeueFn is function used to requeue the item
type RequeueFn = func(context.Context, *Item) error

// immutable
var itemAttributes = []string{ //nolint:gochecknoglobals
	jobs.RRID,
	jobs.RRJob,
	jobs.RRDelay,
	jobs.RRPriority,
	jobs.RRHeaders,
}

type Item struct {
	// Job contains pluginName of job broker (usually PHP class).
	Job string `json:"job"`
	// Ident is unique identifier of the job, should be provided from outside
	Ident string `json:"id"`
	// Payload is string data (usually JSON) passed to Job broker.
	Payload string `json:"payload"`
	// Headers with key-values pairs
	Headers map[string][]string `json:"headers"`
	// Options contains set of PipelineOptions specific to job execution. Can be empty.
	Options *Options `json:"options,omitempty"`
}

// Options carry information about how to handle given job.
type Options struct {
	// Priority is job priority, default - 10
	// pointer to distinguish 0 as a priority and nil as priority not set
	Priority int64 `json:"priority"`
	// Pipeline manually specified pipeline.
	Pipeline string `json:"pipeline,omitempty"`
	// Delay defines time duration to delay execution for. Defaults to none.
	Delay int64 `json:"delay,omitempty"`
	// AutoAck jobs after receive it from the queue
	AutoAck bool `json:"auto_ack"`

	// Private ================
	cond               *sync.Cond
	msgInFlight        *int64
	approxReceiveCount int64
	queue              *string
	receiptHandler     *string
	client             *sqs.Client
	requeueFn          RequeueFn
}

// DelayDuration returns delay duration in a form of time.Duration.
func (o *Options) DelayDuration() time.Duration {
	return time.Second * time.Duration(o.Delay)
}

func (i *Item) ID() string {
	return i.Ident
}

func (i *Item) Priority() int64 {
	return i.Options.Priority
}

func (i *Item) Metadata() map[string][]string {
	return i.Headers
}

// Body packs job payload into binary payload.
func (i *Item) Body() []byte {
	return utils.AsBytes(i.Payload)
}

// Context packs job context (job, id) into binary payload.
// Not used in the sqs, MessageAttributes used instead
func (i *Item) Context() ([]byte, error) {
	ctx, err := json.Marshal(
		struct {
			ID       string              `json:"id"`
			Job      string              `json:"job"`
			Driver   string              `json:"driver"`
			Headers  map[string][]string `json:"headers"`
			Pipeline string              `json:"pipeline"`
		}{
			ID:       i.Ident,
			Job:      i.Job,
			Driver:   pluginName,
			Headers:  i.Headers,
			Pipeline: i.Options.Pipeline,
		},
	)

	if err != nil {
		return nil, err
	}

	return ctx, nil
}

func (i *Item) Ack() error {
	defer func() {
		i.Options.cond.Signal()
		atomic.AddInt64(i.Options.msgInFlight, ^int64(0))
	}()
	// just return in case of auto-ack
	if i.Options.AutoAck {
		return nil
	}
	_, err := i.Options.client.DeleteMessage(context.Background(), &sqs.DeleteMessageInput{
		QueueUrl:      i.Options.queue,
		ReceiptHandle: i.Options.receiptHandler,
	})

	if err != nil {
		return err
	}

	return nil
}

func (i *Item) Nack() error {
	defer func() {
		i.Options.cond.Signal()
		atomic.AddInt64(i.Options.msgInFlight, ^int64(0))
	}()
	// message already deleted
	if i.Options.AutoAck {
		return nil
	}

	// requeue message
	err := i.Options.requeueFn(context.Background(), i)
	if err != nil {
		return err
	}

	_, err = i.Options.client.DeleteMessage(context.Background(), &sqs.DeleteMessageInput{
		QueueUrl:      i.Options.queue,
		ReceiptHandle: i.Options.receiptHandler,
	})

	if err != nil {
		return err
	}

	return nil
}

func (i *Item) Requeue(headers map[string][]string, delay int64) error {
	defer func() {
		i.Options.cond.Signal()
		atomic.AddInt64(i.Options.msgInFlight, ^int64(0))
	}()
	// overwrite the delay
	i.Options.Delay = delay
	i.Headers = headers

	// requeue message
	err := i.Options.requeueFn(context.Background(), i)
	if err != nil {
		return err
	}

	// in case of auto_ack a message was already deleted from the queue
	if !i.Options.AutoAck {
		// Delete job from the queue only after successful requeue
		_, err = i.Options.client.DeleteMessage(context.Background(), &sqs.DeleteMessageInput{
			QueueUrl:      i.Options.queue,
			ReceiptHandle: i.Options.receiptHandler,
		})

		if err != nil {
			return err
		}
	}

	return nil
}

func (i *Item) Respond(_ []byte, _ string) error {
	return nil
}

func fromJob(job jobs.Job) *Item {
	return &Item{
		Job:     job.Name(),
		Ident:   job.ID(),
		Payload: job.Payload(),
		Headers: job.Headers(),
		Options: &Options{
			Priority: job.Priority(),
			Pipeline: job.Pipeline(),
			Delay:    job.Delay(),
			AutoAck:  job.AutoAck(),
		},
	}
}

func (i *Item) pack(queueURL, origQueue *string, mg string) (*sqs.SendMessageInput, error) {
	// pack headers map
	data, err := json.Marshal(i.Headers)
	if err != nil {
		return nil, err
	}

	return &sqs.SendMessageInput{
		MessageBody:            aws.String(i.Payload),
		QueueUrl:               queueURL,
		DelaySeconds:           delay(origQueue, int32(i.Options.Delay)),
		MessageDeduplicationId: dedup(i.ID(), origQueue),
		// message group used for the FIFO
		MessageGroupId: mgr(mg),
		MessageAttributes: map[string]types.MessageAttributeValue{
			jobs.RRID:       {DataType: aws.String(StringType), BinaryValue: nil, BinaryListValues: nil, StringListValues: nil, StringValue: aws.String(i.Ident)},
			jobs.RRJob:      {DataType: aws.String(StringType), BinaryValue: nil, BinaryListValues: nil, StringListValues: nil, StringValue: aws.String(i.Job)},
			jobs.RRDelay:    {DataType: aws.String(StringType), BinaryValue: nil, BinaryListValues: nil, StringListValues: nil, StringValue: aws.String(strconv.Itoa(int(i.Options.Delay)))},
			jobs.RRHeaders:  {DataType: aws.String(BinaryType), BinaryValue: data, BinaryListValues: nil, StringListValues: nil, StringValue: nil},
			jobs.RRPriority: {DataType: aws.String(NumberType), BinaryValue: nil, BinaryListValues: nil, StringListValues: nil, StringValue: aws.String(strconv.Itoa(int(i.Options.Priority)))},
			jobs.RRAutoAck:  {DataType: aws.String(StringType), BinaryValue: nil, BinaryListValues: nil, StringListValues: nil, StringValue: aws.String(btos(i.Options.AutoAck))},
		},
	}, nil
}

func (c *Driver) fromMsg(msg *types.Message) (*Item, error) {
	item, err := c.unpack(msg)
	if err == nil {
		return item, nil
	}

	switch {
	case errors.Is(errors.Decode, err) && c.consumeAll:
		id := uuid.NewString()
		c.log.Debug("get raw payload", zap.String("assigned ID", id))

		var approxRecCount int
		if val, ok := msg.Attributes[ApproximateReceiveCount]; ok {
			approxRecCount, _ = strconv.Atoi(val)
		}

		switch {
		case msg.Body != nil:
			var data []byte
			if isJSONEncoded(msg.Body) != nil {
				data, err = json.Marshal(msg.Body)
				if err != nil {
					return nil, err
				}
			}

			return &Item{
				Job:     auto,
				Ident:   id,
				Payload: utils.AsString(data),
				Headers: convAttr(msg.Attributes),
				Options: &Options{
					Priority:           10,
					Pipeline:           auto,
					AutoAck:            false,
					approxReceiveCount: int64(approxRecCount),
					queue:              c.queue,
					receiptHandler:     msg.ReceiptHandle,
					client:             c.client,
					requeueFn:          c.handleItem,
					// 2.12.1
					msgInFlight: c.msgInFlight,
					cond:        &c.cond,
				},
			}, nil
		default:
			return &Item{
				Job:     auto,
				Ident:   id,
				Payload: checkBody(msg.Body),
				Headers: convAttr(msg.Attributes),
				Options: &Options{
					Priority:           10,
					Pipeline:           auto,
					AutoAck:            false,
					approxReceiveCount: int64(approxRecCount),
					queue:              c.queue,
					receiptHandler:     msg.ReceiptHandle,
					client:             c.client,
					requeueFn:          c.handleItem,
					// 2.12.1
					msgInFlight: c.msgInFlight,
					cond:        &c.cond,
				},
			}, nil
		}
	default:
		c.log.Debug("failed to parse the message, might be should turn on: `consume_all:true` ?")
		return nil, err
	}
}

func (c *Driver) unpack(msg *types.Message) (*Item, error) {
	const op = errors.Op("sqs_unpack")
	// reserved
	if _, ok := msg.Attributes[ApproximateReceiveCount]; !ok {
		return nil, errors.E(op, errors.Str("failed to unpack the ApproximateReceiveCount attribute"), errors.Decode)
	}

	for i := 0; i < len(itemAttributes); i++ {
		if _, ok := msg.MessageAttributes[itemAttributes[i]]; !ok {
			return nil, errors.E(op, errors.Errorf("missing queue attribute: %s", itemAttributes[i]), errors.Decode)
		}
	}

	var h map[string][]string
	err := json.Unmarshal(msg.MessageAttributes[jobs.RRHeaders].BinaryValue, &h)
	if err != nil {
		return nil, errors.E(op, errors.Decode)
	}

	d, err := strconv.Atoi(*msg.MessageAttributes[jobs.RRDelay].StringValue)
	if err != nil {
		return nil, errors.E(op, err, errors.Decode)
	}

	priority, err := strconv.Atoi(*msg.MessageAttributes[jobs.RRPriority].StringValue)
	if err != nil {
		return nil, errors.E(op, err, errors.Decode)
	}

	recCount, err := strconv.Atoi(msg.Attributes[ApproximateReceiveCount])
	if err != nil {
		return nil, errors.E(op, err, errors.Decode)
	}

	// for the existing messages, auto_ack field might be absent
	var autoAck bool
	if aa, ok := msg.MessageAttributes[jobs.RRAutoAck]; ok {
		autoAck = stob(aa.StringValue)
	}

	item := &Item{
		Job:     *msg.MessageAttributes[jobs.RRJob].StringValue,
		Ident:   *msg.MessageAttributes[jobs.RRID].StringValue,
		Payload: *msg.Body,
		Headers: h,
		Options: &Options{
			AutoAck:  autoAck,
			Delay:    int64(d),
			Priority: int64(priority),

			// private
			approxReceiveCount: int64(recCount),
			client:             c.client,
			queue:              c.queueURL,
			receiptHandler:     msg.ReceiptHandle,
			requeueFn:          c.handleItem,
			// 2.12.1
			msgInFlight: c.msgInFlight,
			cond:        &c.cond,
		},
	}

	return item, nil
}

func mgr(gr string) *string {
	if gr == "" {
		return nil
	}
	return aws.String(gr)
}

func dedup(d string, origQueue *string) *string {
	if strings.HasSuffix(*origQueue, fifoSuffix) {
		if d == "" {
			return aws.String(uuid.NewString())
		}

		return aws.String(d)
	}

	return nil
}

func delay(origQueue *string, delay int32) int32 {
	if strings.HasSuffix(*origQueue, fifoSuffix) {
		return 0
	}

	return delay
}

func btos(b bool) string {
	if b {
		return "true"
	}

	return "false"
}

func stob(s *string) bool {
	if s != nil {
		return *s == "true"
	}

	return false
}

func checkBody(body *string) string {
	if body == nil {
		return ""
	}
	return *body
}

func isJSONEncoded(data *string) error {
	var a any
	return json.Unmarshal(utils.AsBytes(*data), &a)
}
