package sqsjobs

import (
	"context"
	stderr "errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/roadrunner-server/api/v4/plugins/v4/jobs"
	"github.com/roadrunner-server/errors"
	jprop "go.opentelemetry.io/contrib/propagators/jaeger"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

const (
	pluginName   string = "sqs"
	tracerName   string = "jobs"
	assumeAWSEnv string = "No sqs plugin configuration section was found; assuming we're in AWS environment and want to use default values."
)

var _ jobs.Driver = (*Driver)(nil)

type Configurer interface {
	// UnmarshalKey takes a single key and unmarshal it into a Struct.
	UnmarshalKey(name string, out any) error
	// Has checks if a config section exists.
	Has(name string) bool
}

type Driver struct {
	mu               sync.Mutex
	cond             sync.Cond
	msgInFlight      *int64
	msgInFlightLimit *int32

	pq          jobs.Queue
	log         *zap.Logger
	pipeline    atomic.Pointer[jobs.Pipeline]
	skipDeclare bool

	tracer *sdktrace.TracerProvider
	prop   propagation.TextMapPropagator

	// func to cancel listener
	cancel context.CancelFunc

	// connection info
	queue                  *string
	messageGroupID         string
	waitTime               int32
	visibilityTimeout      int32
	errorVisibilityTimeout int32
	prefetch               int32
	retainFailedJobs       bool

	// if a user invokes several resume operations
	listeners uint32

	// queue optional parameters
	attributes map[string]string
	tags       map[string]string

	client   *sqs.Client
	queueURL *string

	stopped uint64
	pauseCh chan struct{}
}

func FromConfig(tracer *sdktrace.TracerProvider, configKey string, pipe jobs.Pipeline, log *zap.Logger, cfg Configurer, pq jobs.Queue) (*Driver, error) {
	const op = errors.Op("new_sqs_consumer")

	// if no such key - error
	if !cfg.Has(configKey) {
		return nil, errors.E(op, errors.Errorf("no configuration by provided key: %s", configKey))
	}

	if tracer == nil {
		tracer = sdktrace.NewTracerProvider()
	}

	prop := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}, jprop.Jaeger{})
	otel.SetTextMapPropagator(prop)

	// PARSE CONFIGURATION -------
	var conf Config
	err := cfg.UnmarshalKey(configKey, &conf)
	if err != nil {
		return nil, errors.E(op, err)
	}

	// parse global config if exists
	if cfg.Has(pluginName) {
		err = cfg.UnmarshalKey(pluginName, &conf)
		if err != nil {
			return nil, errors.E(op, err)
		}
	} else {
		log.Info(assumeAWSEnv)
	}

	conf.InitDefault()

	// initialize job Driver
	jb := &Driver{
		tracer:                 tracer,
		prop:                   prop,
		cond:                   sync.Cond{L: &sync.Mutex{}},
		pq:                     pq,
		log:                    log,
		skipDeclare:            conf.SkipQueueDeclaration,
		messageGroupID:         conf.MessageGroupID,
		attributes:             conf.Attributes,
		tags:                   conf.Tags,
		queue:                  conf.Queue,
		visibilityTimeout:      conf.VisibilityTimeout,
		errorVisibilityTimeout: conf.ErrorVisibilityTimeout,
		retainFailedJobs:       conf.RetainFailedJobs,
		waitTime:               conf.WaitTimeSeconds,
		prefetch:               conf.Prefetch,
		pauseCh:                make(chan struct{}, 1),
		// new in 2.12.1
		msgInFlightLimit: &conf.MaxMsgInFlightLimit,
		msgInFlight:      ptr(int64(0)),
	}

	// PARSE CONFIGURATION -------
	jb.client, err = checkEnv(conf.Key, conf.Secret, conf.SessionToken, conf.Endpoint, conf.Region)
	if err != nil {
		return nil, errors.E(op, err)
	}

	// if the queue is already declared and user do not want to
	err = manageQueue(jb)
	if err != nil {
		return nil, errors.E(op, err)
	}

	jb.pipeline.Store(&pipe)

	// To successfully create a new queue, you must provide a
	// queue name that adheres to the limits related to queues
	// (https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/limits-queues.html)
	// and is unique within the scope of your queues. After you create a queue, you
	// must wait at least one second after the queue is created to be able to use the <------------
	// queue. To get the queue URL, use the GetQueueUrl action. GetQueueUrl require
	time.Sleep(time.Second)

	return jb, nil
}

func FromPipeline(tracer *sdktrace.TracerProvider, pipe jobs.Pipeline, log *zap.Logger, cfg Configurer, pq jobs.Queue) (*Driver, error) {
	const op = errors.Op("new_sqs_consumer")

	if tracer == nil {
		tracer = sdktrace.NewTracerProvider()
	}

	prop := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}, jprop.Jaeger{})
	otel.SetTextMapPropagator(prop)

	// PARSE CONFIGURATION -------
	var conf Config

	// parse global config if exists
	if cfg.Has(pluginName) {
		err := cfg.UnmarshalKey(pluginName, &conf)
		if err != nil {
			return nil, errors.E(op, err)
		}
	} else {
		log.Info(assumeAWSEnv)
	}

	conf.InitDefault()

	attr := make(map[string]string)
	err := pipe.Map(attributes, attr)
	if err != nil {
		return nil, errors.E(op, err)
	}

	tg := make(map[string]string)
	err = pipe.Map(tags, tg)
	if err != nil {
		return nil, errors.E(op, err)
	}

	wt := pipe.Int(waitTime, 0)
	if wt < 0 {
		wt = 0
	} else if wt > int(maxWaitTime) {
		wt = int(maxWaitTime)
	}

	pref := int32(pipe.Int(prefetch, 1))                        //nolint:gosec
	msgInFl := int32(pipe.Int(maxMsgsInFlightLimit, int(pref))) //nolint:gosec

	// initialize job Driver
	jb := &Driver{
		tracer:                 tracer,
		prop:                   prop,
		cond:                   sync.Cond{L: &sync.Mutex{}},
		pq:                     pq,
		log:                    log,
		messageGroupID:         pipe.String(messageGroupID, ""),
		attributes:             attr,
		tags:                   tg,
		skipDeclare:            pipe.Bool(skipQueueDeclaration, false),
		queue:                  aws.String(pipe.String(queue, "default")),
		visibilityTimeout:      int32(pipe.Int(visibility, 0)),             //nolint:gosec
		errorVisibilityTimeout: int32(pipe.Int(errorVisibilityTimeout, 0)), //nolint:gosec
		retainFailedJobs:       pipe.Bool(retainFailedJobs, false),
		waitTime:               int32(wt), //nolint:gosec
		prefetch:               pref,
		pauseCh:                make(chan struct{}, 1),
		// new in 2.12.1
		// default - prefetch
		msgInFlightLimit: ptr(msgInFl), //nolin:gosec
		msgInFlight:      ptr(int64(0)),
	}

	// PARSE CONFIGURATION -------

	jb.client, err = checkEnv(conf.Key, conf.Secret, conf.SessionToken, conf.Endpoint, conf.Region)
	if err != nil {
		return nil, errors.E(op, err)
	}

	// if the queue is already declared and user do not want to
	err = manageQueue(jb)
	if err != nil {
		return nil, errors.E(op, err)
	}

	jb.pipeline.Store(&pipe)
	// To successfully create a new queue, you must provide a
	// queue name that adheres to the limits related to queues
	// (https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/limits-queues.html)
	// and is unique within the scope of your queues. After you create a queue, you
	// must wait at least one second after the queue is created to be able to use the <------------
	// queue. To get the queue URL, use the GetQueueUrl action. GetQueueUrl require
	time.Sleep(time.Second)

	return jb, nil
}

func (c *Driver) Push(ctx context.Context, jb jobs.Message) error {
	const op = errors.Op("sqs_push")
	// check if the pipeline registered

	ctx, span := trace.SpanFromContext(ctx).TracerProvider().Tracer(tracerName).Start(ctx, "sqs_push")
	defer span.End()

	// load atomic value
	pipe := *c.pipeline.Load()
	if pipe.Name() != jb.GroupID() {
		return errors.E(op, errors.Errorf("no such pipeline: %s, actual: %s", jb.GroupID(), pipe.Name()))
	}

	// The length of time, in seconds, for which to delay a specific message. Valid
	// values: 0 to 900. Maximum: 15 minutes.
	if jb.Delay() > 900 {
		return errors.E(op, errors.Errorf("unable to push, maximum possible delay is 900 seconds (15 minutes), provided: %d", jb.Delay()))
	}

	err := c.handleItem(ctx, fromJob(jb))
	if err != nil {
		return errors.E(op, err)
	}

	return nil
}

func (c *Driver) Run(ctx context.Context, p jobs.Pipeline) error {
	start := time.Now().UTC()
	const op = errors.Op("sqs_run")

	_, span := trace.SpanFromContext(ctx).TracerProvider().Tracer(tracerName).Start(ctx, "sqs_run")
	defer span.End()

	c.mu.Lock()
	defer c.mu.Unlock()

	pipe := *c.pipeline.Load()
	if pipe.Name() != p.Name() {
		return errors.E(op, errors.Errorf("no such pipeline registered: %s", pipe.Name()))
	}

	atomic.AddUint32(&c.listeners, 1)

	// start listener
	var ctxCancel context.Context
	ctxCancel, c.cancel = context.WithCancel(context.Background())
	c.listen(ctxCancel)

	c.log.Debug("pipeline was started", zap.String("driver", pipe.Driver()), zap.String("pipeline", pipe.Name()), zap.Time("start", start), zap.Int64("elapsed", time.Since(start).Milliseconds()))
	return nil
}

func (c *Driver) Stop(ctx context.Context) error {
	start := time.Now().UTC()

	_, span := trace.SpanFromContext(ctx).TracerProvider().Tracer(tracerName).Start(ctx, "sqs_stop")
	defer span.End()

	atomic.StoreUint64(&c.stopped, 1)
	pipe := *c.pipeline.Load()
	_ = c.pq.Remove(pipe.Name())

	if atomic.LoadUint32(&c.listeners) > 0 {
		if c.cancel != nil {
			c.cancel()
		}
		// if blocked, let 1 item to pass to unblock the listener and close the pipe
		c.cond.Signal()
		// we're expecting that the listener will receive the signal and close the channel
		c.pauseCh <- struct{}{}
	}

	c.log.Debug("pipeline was stopped", zap.String("driver", pipe.Driver()), zap.String("pipeline", pipe.Name()), zap.Time("start", time.Now().UTC()), zap.Int64("elapsed", time.Since(start).Milliseconds()))
	return nil
}

func (c *Driver) Pause(ctx context.Context, p string) error {
	start := time.Now().UTC()

	_, span := trace.SpanFromContext(ctx).TracerProvider().Tracer(tracerName).Start(ctx, "sqs_pause")
	defer span.End()

	// load atomic value
	pipe := *c.pipeline.Load()
	if pipe.Name() != p {
		return errors.Errorf("no such pipeline: %s", pipe.Name())
	}

	l := atomic.LoadUint32(&c.listeners)
	// no active listeners
	if l == 0 {
		return errors.Str("no active listeners, nothing to pause")
	}

	atomic.AddUint32(&c.listeners, ^uint32(0))

	if c.cancel != nil {
		c.cancel()
	}

	// stop consume
	c.pauseCh <- struct{}{}
	// if blocked, let 1 item to pass to unblock the listener and close the pipe
	c.cond.Signal()

	c.log.Debug("pipeline was paused", zap.String("driver", pipe.Driver()), zap.String("pipeline", pipe.Name()), zap.Time("start", time.Now().UTC()), zap.Int64("elapsed", time.Since(start).Milliseconds()))

	return nil
}

func (c *Driver) Resume(ctx context.Context, p string) error {
	start := time.Now().UTC()

	_, span := trace.SpanFromContext(ctx).TracerProvider().Tracer(tracerName).Start(ctx, "sqs_resume")
	defer span.End()

	c.mu.Lock()
	defer c.mu.Unlock()

	// load atomic value
	pipe := *c.pipeline.Load()
	if pipe.Name() != p {
		return errors.Errorf("no such pipeline: %s", pipe.Name())
	}

	l := atomic.LoadUint32(&c.listeners)
	// no active listeners
	if l == 1 {
		return errors.Str("sqs listener is already in the active state")
	}

	// start listener
	var ctxCancel context.Context
	ctxCancel, c.cancel = context.WithCancel(context.Background())
	c.listen(ctxCancel)

	// increase num of listeners
	atomic.AddUint32(&c.listeners, 1)
	c.log.Debug("pipeline was resumed", zap.String("driver", pipe.Driver()), zap.String("pipeline", pipe.Name()), zap.Time("start", time.Now().UTC()), zap.Int64("elapsed", time.Since(start).Milliseconds()))

	return nil
}

func (c *Driver) State(ctx context.Context) (*jobs.State, error) {
	const op = errors.Op("sqs_state")

	ctx, span := trace.SpanFromContext(ctx).TracerProvider().Tracer(tracerName).Start(ctx, "sqs_state")
	defer span.End()

	attr, err := c.client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: c.queueURL,
		AttributeNames: []types.QueueAttributeName{
			types.QueueAttributeNameApproximateNumberOfMessages,
			types.QueueAttributeNameApproximateNumberOfMessagesDelayed,
			types.QueueAttributeNameApproximateNumberOfMessagesNotVisible,
		},
	})

	if err != nil {
		return nil, errors.E(op, err)
	}

	pipe := *c.pipeline.Load()

	out := &jobs.State{
		Priority: uint64(pipe.Priority()), //nolint:gosec
		Pipeline: pipe.Name(),
		Driver:   pipe.Driver(),
		Queue:    *c.queueURL,
		Ready:    ready(atomic.LoadUint32(&c.listeners)),
	}

	nom, err := strconv.Atoi(attr.Attributes[string(types.QueueAttributeNameApproximateNumberOfMessages)])
	if err == nil {
		out.Active = int64(nom)
	}

	delayed, err := strconv.Atoi(attr.Attributes[string(types.QueueAttributeNameApproximateNumberOfMessagesDelayed)])
	if err == nil {
		out.Delayed = int64(delayed)
	}

	nv, err := strconv.Atoi(attr.Attributes[string(types.QueueAttributeNameApproximateNumberOfMessagesNotVisible)])
	if err == nil {
		out.Reserved = int64(nv)
	}

	return out, nil
}

func (c *Driver) handleItem(ctx context.Context, msg *Item) error {
	c.prop.Inject(ctx, propagation.HeaderCarrier(msg.headers))

	d, err := msg.pack(c.queueURL, c.queue, c.messageGroupID)
	if err != nil {
		return err
	}

	_, err = c.client.SendMessage(ctx, d)
	if err != nil {
		return err
	}

	return nil
}

func checkEnv(key, secret, sessionToken, endpoint, region string) (*sqs.Client, error) {
	const op = errors.Op("check_env")
	var client *sqs.Client
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	opts := make([]func(*config.LoadOptions) error, 0, 1)

	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}

	// session_token is optional; if no credentials are provided, we assume user wants to default to AWS IAM.
	if secret != "" && key != "" {
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(key, secret, sessionToken)))
	}

	if endpoint != "" {
		// Setting the endpoint is only necessary when self-hosting the queue, as region (either from AWS env or
		// set in rr config) will tell us which AWS SQS endpoint to use.
		opts = append(opts, config.WithBaseEndpoint(endpoint))
	}

	// Load default credentials; if in AWS context, this will auth as the associated IAM role.
	// We override this config with user-provided values, if any.
	awsConf, err := config.LoadDefaultConfig(ctx, opts...)

	if err != nil {
		return nil, errors.E(op, err)
	}

	// config with retries
	client = sqs.NewFromConfig(awsConf, func(o *sqs.Options) {
		o.Retryer = retry.NewStandard(func(opts *retry.StandardOptions) {
			opts.MaxAttempts = 60
			opts.MaxBackoff = time.Second * 2
		})
	})

	return client, nil
}

func manageQueue(jb *Driver) error {
	var err error
	switch jb.skipDeclare {
	case true:
		jb.queueURL, err = getQueueURL(jb.client, jb.queue)
		if err != nil {
			return err
		}
	case false:
		jb.queueURL, err = createQueue(jb.client, jb.queue, jb.attributes, jb.tags)
		if err != nil {
			return err
		}
	}

	return nil
}

func createQueue(client *sqs.Client, queueName *string, attributes map[string]string, tags map[string]string) (*string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()
	out, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: queueName, Attributes: attributes, Tags: tags})
	if err != nil {
		var qErr *types.QueueNameExists
		if stderr.As(err, &qErr) {
			// Queue already exists; return existing URL instead.
			return getQueueURL(client, queueName)
		}

		return nil, err
	}

	return out.QueueUrl, nil
}

func getQueueURL(client *sqs.Client, queueName *string) (*string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()
	out, err := client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: queueName})
	if err != nil {
		return nil, err
	}

	return out.QueueUrl, nil
}

func ptr[T any](val T) *T {
	return &val
}

func ready(r uint32) bool {
	return r > 0
}

func bytesToStr(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	return unsafe.String(unsafe.SliceData(data), len(data))
}
