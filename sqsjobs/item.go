package sqsjobs

import (
	"context"
	"log"
	"maps"
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
	"github.com/roadrunner-server/api/v4/plugins/v4/jobs"
	"github.com/roadrunner-server/errors"
	"go.uber.org/zap"

	goErrors "errors"
)

const (
	StringType              string = "String"
	NumberType              string = "Number"
	BinaryType              string = "Binary"
	ApproximateReceiveCount string = "ApproximateReceiveCount"
	fifoSuffix              string = ".fifo"
)

var _ jobs.Job = (*Item)(nil)

// RequeueFn is used to requeue the item
type RequeueFn = func(context.Context, *Item) error

type Item struct {
	// Job contains the pluginName of job broker (usually PHP class).
	Job string `json:"job"`
	// Ident is a unique identifier of the job, should be provided from outside
	Ident string `json:"id"`
	// Payload is string data (usually JSON) passed to Job broker.
	Payload []byte `json:"payload"`
	// Headers with key-values pairs
	headers map[string][]string
	// Options contain a set of PipelineOptions specific to job execution. Can be empty.
	Options *Options `json:"options,omitempty"`
}

// Options carry information about how to handle a given job.
type Options struct {
	// Priority is job priority, default - 10
	// pointer to distinguish 0 as a priority and nil as a priority not set
	Priority int64 `json:"priority"`
	// Pipeline manually specified pipeline.
	Pipeline string `json:"pipeline,omitempty"`
	// Delay defines time duration to delay execution for. Defaults to none.
	Delay int `json:"delay,omitempty"`
	// AutoAck jobs after receive it from the queue
	AutoAck bool `json:"auto_ack"`
	// SQS Queue name
	Queue string `json:"queue,omitempty"`
	// If RetainFailedJobs is true, failed jobs will have their visibility timeout set to this value instead of the
	// default VisibilityTimeout.
	ErrorVisibilityTimeout int32 `json:"error_visibility_timeout,omitempty"`
	// Whether to retain failed jobs on the queue. If true, jobs will not be deleted and re-queued on NACK.
	RetainFailedJobs bool `json:"retain_failed_jobs,omitempty"`

	// Private ================
	cond               *sync.Cond
	stopped            *uint64
	msgInFlight        *int64
	approxReceiveCount int64
	queue              *string
	receiptHandler     *string
	client             *sqs.Client
	requeueFn          RequeueFn
}

// DelayDuration returns delay duration in the form of time.Duration.
func (o *Options) DelayDuration() time.Duration {
	return time.Second * time.Duration(o.Delay)
}

func (i *Item) ID() string {
	return i.Ident
}

func (i *Item) Priority() int64 {
	return i.Options.Priority
}

func (i *Item) GroupID() string {
	return i.Options.Pipeline
}

func (i *Item) Headers() map[string][]string {
	return i.headers
}

// Body packs job payload into binary payload.
func (i *Item) Body() []byte {
	return i.Payload
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
			Queue    string              `json:"queue,omitempty"`
			Pipeline string              `json:"pipeline"`
		}{
			ID:       i.Ident,
			Job:      i.Job,
			Driver:   pluginName,
			Headers:  i.headers,
			Queue:    i.Options.Queue,
			Pipeline: i.Options.Pipeline,
		},
	)

	if err != nil {
		return nil, err
	}

	return ctx, nil
}

func (i *Item) Ack() error {
	if atomic.LoadUint64(i.Options.stopped) == 1 {
		return errors.Str("failed to acknowledge the JOB, the pipeline is probably stopped")
	}
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

func CommonNack(i *Item) error {
	if atomic.LoadUint64(i.Options.stopped) == 1 {
		return errors.Str("failed to acknowledge the JOB, the pipeline is probably stopped")
	}
	defer func() {
		i.Options.cond.Signal()
		atomic.AddInt64(i.Options.msgInFlight, ^int64(0))
	}()
	// message already deleted
	if i.Options.AutoAck {
		return nil
	}

	if !i.Options.RetainFailedJobs {
		// requeue as new message
		err := i.Options.requeueFn(context.Background(), i)
		if err != nil {
			return err
		}
		// Delete original message
		_, err = i.Options.client.DeleteMessage(context.Background(), &sqs.DeleteMessageInput{
			QueueUrl:      i.Options.queue,
			ReceiptHandle: i.Options.receiptHandler,
		})

		if err != nil {
			return err
		}
	} else if i.Options.ErrorVisibilityTimeout > 0 {
		// If error visibility is defined change the visibility timeout of the job that failed
		_, err := i.Options.client.ChangeMessageVisibility(context.Background(), &sqs.ChangeMessageVisibilityInput{
			QueueUrl:          i.Options.queue,
			ReceiptHandle:     i.Options.receiptHandler,
			VisibilityTimeout: i.Options.ErrorVisibilityTimeout,
		})

		if err != nil {
			var notInFlight *types.MessageNotInflight
			if goErrors.As(err, &notInFlight) {
				// I would like to log info/warning here, but not sure how to get the correct logger
				log.Println("MessageNotInFlight; ignoring ChangeMessageVisibility")
			} else {
				return err
			}
		}
	} // else dont do anything; wait for VisibilityTimeout to expire.

	return nil
}

func (i *Item) Nack() error {
	return CommonNack(i)
}

func (i *Item) NackWithOptions(requeue bool, delay int) error {
	if requeue {
		// requeue message
		err := i.Requeue(nil, delay)
		if err != nil {
			return err
		}

		return nil
	}
	return CommonNack(i)
}

func (i *Item) Requeue(headers map[string][]string, delay int) error {
	if atomic.LoadUint64(i.Options.stopped) == 1 {
		return errors.Str("failed to acknowledge the JOB, the pipeline is probably stopped")
	}

	defer func() {
		i.Options.cond.Signal()
		atomic.AddInt64(i.Options.msgInFlight, ^int64(0))
	}()

	// overwrite the delay
	i.Options.Delay = delay
	if i.headers == nil {
		i.headers = make(map[string][]string)
	}

	if len(headers) > 0 {
		maps.Copy(i.headers, headers)
	}

	// requeue message
	err := i.Options.requeueFn(context.Background(), i)
	if err != nil {
		return err
	}

	// in case of auto_ack a message was already deleted from the queue
	if !i.Options.AutoAck {
		// Delete the job from the queue only after the successful requeue
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

func fromJob(job jobs.Message) *Item {
	return &Item{
		Job:     job.Name(),
		Ident:   job.ID(),
		Payload: job.Payload(),
		headers: job.Headers(),
		Options: &Options{
			Priority: job.Priority(),
			Pipeline: job.GroupID(),
			Delay:    int(job.Delay()),
			AutoAck:  job.AutoAck(),
		},
	}
}

func (i *Item) pack(queueURL, origQueue *string, mg string) (*sqs.SendMessageInput, error) {
	// pack a header map
	data, err := json.Marshal(i.headers)
	if err != nil {
		return nil, err
	}

	return &sqs.SendMessageInput{
		MessageBody:            aws.String(bytesToStr(i.Payload)),
		QueueUrl:               queueURL,
		DelaySeconds:           delay(origQueue, int32(i.Options.Delay)), //nolint:gosec
		MessageDeduplicationId: dedup(i.ID(), origQueue),
		// message group used for the FIFO
		MessageGroupId: mgr(mg),
		MessageAttributes: map[string]types.MessageAttributeValue{
			jobs.RRID:       {DataType: aws.String(StringType), BinaryValue: nil, BinaryListValues: nil, StringListValues: nil, StringValue: aws.String(i.Ident)},
			jobs.RRJob:      {DataType: aws.String(StringType), BinaryValue: nil, BinaryListValues: nil, StringListValues: nil, StringValue: aws.String(i.Job)},
			jobs.RRDelay:    {DataType: aws.String(StringType), BinaryValue: nil, BinaryListValues: nil, StringListValues: nil, StringValue: aws.String(strconv.Itoa(i.Options.Delay))},
			jobs.RRHeaders:  {DataType: aws.String(BinaryType), BinaryValue: data, BinaryListValues: nil, StringListValues: nil, StringValue: nil},
			jobs.RRPriority: {DataType: aws.String(NumberType), BinaryValue: nil, BinaryListValues: nil, StringListValues: nil, StringValue: aws.String(strconv.Itoa(int(i.Options.Priority)))},
			jobs.RRAutoAck:  {DataType: aws.String(StringType), BinaryValue: nil, BinaryListValues: nil, StringListValues: nil, StringValue: aws.String(btos(i.Options.AutoAck))},
		},
	}, nil
}

func (c *Driver) unpack(msg *types.Message) *Item {
	// reserved
	var recCount int64
	if _, ok := msg.Attributes[ApproximateReceiveCount]; !ok {
		c.log.Debug("failed to unpack the ApproximateReceiveCount attribute, using -1 as a fallback")
	} else {
		tmp, err := strconv.Atoi(msg.Attributes[ApproximateReceiveCount])
		if err != nil {
			c.log.Warn("failed to unpack the ApproximateReceiveCount attribute, using -1 as a fallback", zap.Error(err))
		}
		recCount = int64(tmp)
	}

	h := make(map[string][]string)
	if _, ok := msg.MessageAttributes[jobs.RRHeaders]; ok {
		err := json.Unmarshal(msg.MessageAttributes[jobs.RRHeaders].BinaryValue, &h)
		if err != nil {
			c.log.Debug("failed to unpack the headers, not a JSON", zap.Error(err))
		}
	} else {
		h = convAttr(msg.Attributes)
	}

	var dl int
	var err error
	if _, ok := msg.MessageAttributes[jobs.RRDelay]; ok {
		dl, err = strconv.Atoi(*msg.MessageAttributes[jobs.RRDelay].StringValue)
		if err != nil {
			c.log.Debug("failed to unpack the delay, not a number", zap.Error(err))
		}
	}

	var priority int
	if _, ok := msg.Attributes[jobs.RRPriority]; ok {
		priority, err = strconv.Atoi(*msg.MessageAttributes[jobs.RRPriority].StringValue)
		if err != nil {
			priority = int((*c.pipeline.Load()).Priority())
			c.log.Debug("failed to unpack the priority; inheriting the pipeline's default priority", zap.Error(err))
		}
	}

	// for the existing messages, auto_ack field might be absent
	var autoAck bool
	if aa, ok := msg.MessageAttributes[jobs.RRAutoAck]; ok {
		autoAck = stob(aa.StringValue)
	}

	var rrj string
	if val, ok := msg.MessageAttributes[jobs.RRJob]; ok {
		rrj = *val.StringValue
	} else {
		rrj = auto
	}

	var rrid string
	if val, ok := msg.MessageAttributes[jobs.RRID]; ok {
		rrid = *val.StringValue
	} else {
		rrid = uuid.NewString()
		// if we don't have RRID we assume that we received a third party message
		convMessageAttr(msg.MessageAttributes, &h)
	}

	return &Item{
		Job:     rrj,
		Ident:   rrid,
		Payload: []byte(getordefault(msg.Body)),
		headers: h,
		Options: &Options{
			AutoAck:                autoAck,
			Delay:                  dl,
			Priority:               int64(priority),
			Pipeline:               (*c.pipeline.Load()).Name(),
			Queue:                  getordefault(c.queue),
			ErrorVisibilityTimeout: c.errorVisibilityTimeout,
			RetainFailedJobs:       c.retainFailedJobs,

			// private
			approxReceiveCount: recCount,
			client:             c.client,
			queue:              c.queueURL,
			receiptHandler:     msg.ReceiptHandle,
			requeueFn:          c.handleItem,
			// 2.12.1
			msgInFlight: c.msgInFlight,
			cond:        &c.cond,
			// 2023.2
			stopped: &c.stopped,
		},
	}
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

func getordefault(body *string) string {
	if body == nil {
		return ""
	}
	return *body
}
