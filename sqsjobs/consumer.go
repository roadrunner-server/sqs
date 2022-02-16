package sqsjobs

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"
	cfgPlugin "github.com/roadrunner-server/api/v2/plugins/config"
	"github.com/roadrunner-server/api/v2/plugins/jobs"
	"github.com/roadrunner-server/api/v2/plugins/jobs/pipeline"
	priorityqueue "github.com/roadrunner-server/api/v2/pq"
	"github.com/roadrunner-server/errors"
	"go.uber.org/zap"
)

const (
	pluginName     string = "sqs"
	awsMetaDataURL string = "http://169.254.169.254/latest/dynamic/instance-identity/"
)

type Consumer struct {
	sync.Mutex
	pq       priorityqueue.Queue
	log      *zap.Logger
	pipeline atomic.Value

	// connection info
	queue             *string
	messageGroupID    string
	waitTime          int32
	prefetch          int32
	visibilityTimeout int32

	// if user invoke several resume operations
	listeners uint32

	// queue optional parameters
	attributes map[string]string
	tags       map[string]string

	client   *sqs.Client
	queueURL *string

	pauseCh chan struct{}
}

func NewSQSConsumer(configKey string, log *zap.Logger, cfg cfgPlugin.Configurer, pq priorityqueue.Queue) (*Consumer, error) {
	const op = errors.Op("new_sqs_consumer")

	/*
		we need to determine in what environment we are running
		1. Non-AWS - global sqs config should be set
		2. AWS - configuration should be obtained from the env
	*/
	insideAWS := false
	if isInAWS() {
		insideAWS = true
	}

	// if no such key - error
	if !cfg.Has(configKey) {
		return nil, errors.E(op, errors.Errorf("no configuration by provided key: %s", configKey))
	}

	// if no global section - try to fetch IAM creds
	if !cfg.Has(pluginName) && !insideAWS {
		return nil, errors.E(op, errors.Str("no global sqs configuration, global configuration should contain sqs section"))
	}

	// PARSE CONFIGURATION -------
	var conf Config
	err := cfg.UnmarshalKey(configKey, &conf)
	if err != nil {
		return nil, errors.E(op, err)
	}

	// parse global config for the local env
	if !insideAWS {
		err = cfg.UnmarshalKey(pluginName, &conf)
		if err != nil {
			return nil, errors.E(op, err)
		}
	}

	conf.InitDefault()

	// initialize job Consumer
	jb := &Consumer{
		pq:                pq,
		log:               log,
		messageGroupID:    conf.MessageGroupID,
		attributes:        conf.Attributes,
		tags:              conf.Tags,
		queue:             conf.Queue,
		prefetch:          conf.Prefetch,
		visibilityTimeout: conf.VisibilityTimeout,
		waitTime:          conf.WaitTimeSeconds,
		pauseCh:           make(chan struct{}, 1),
	}

	// PARSE CONFIGURATION -------
	jb.client, err = checkEnv(insideAWS, conf.Key, conf.Secret, conf.SessionToken, conf.Endpoint, conf.Region)
	if err != nil {
		return nil, errors.E(op, err)
	}

	jb.queueURL, err = createQueue(jb.client, jb.queue, jb.attributes, jb.tags)
	if err != nil {
		return nil, errors.E(op, err)
	}

	// To successfully create a new queue, you must provide a
	// queue name that adheres to the limits related to queues
	// (https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/limits-queues.html)
	// and is unique within the scope of your queues. After you create a queue, you
	// must wait at least one second after the queue is created to be able to use the <------------
	// queue. To get the queue URL, use the GetQueueUrl action. GetQueueUrl require
	time.Sleep(time.Second * 2)

	return jb, nil
}

func FromPipeline(pipe *pipeline.Pipeline, log *zap.Logger, cfg cfgPlugin.Configurer, pq priorityqueue.Queue) (*Consumer, error) {
	const op = errors.Op("new_sqs_consumer")

	/*
		we need to determine in what environment we are running
		1. Non-AWS - global sqs config should be set
		2. AWS - configuration should be obtained from the env
	*/
	insideAWS := false
	if isInAWS() {
		insideAWS = true
	}

	// if no global section
	if !cfg.Has(pluginName) && !insideAWS {
		return nil, errors.E(op, errors.Str("no global sqs configuration, global configuration should contain sqs section"))
	}

	// PARSE CONFIGURATION -------
	conf := &Config{}
	if !insideAWS {
		err := cfg.UnmarshalKey(pluginName, &conf)
		if err != nil {
			return nil, errors.E(op, err)
		}
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

	// initialize job Consumer
	jb := &Consumer{
		pq:                pq,
		log:               log,
		messageGroupID:    pipe.String(messageGroupID, ""),
		attributes:        attr,
		tags:              tg,
		queue:             aws.String(pipe.String(queue, "default")),
		prefetch:          int32(pipe.Int(pref, 10)),
		visibilityTimeout: int32(pipe.Int(visibility, 0)),
		waitTime:          int32(pipe.Int(waitTime, 0)),
		pauseCh:           make(chan struct{}, 1),
	}

	// PARSE CONFIGURATION -------

	jb.client, err = checkEnv(insideAWS, conf.Key, conf.Secret, conf.SessionToken, conf.Endpoint, conf.Region)
	if err != nil {
		return nil, errors.E(op, err)
	}

	jb.queueURL, err = createQueue(jb.client, jb.queue, jb.attributes, jb.tags)
	if err != nil {
		return nil, errors.E(op, err)
	}

	// To successfully create a new queue, you must provide a
	// queue name that adheres to the limits related to queues
	// (https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/limits-queues.html)
	// and is unique within the scope of your queues. After you create a queue, you
	// must wait at least one second after the queue is created to be able to use the <------------
	// queue. To get the queue URL, use the GetQueueUrl action. GetQueueUrl require
	time.Sleep(time.Second)

	return jb, nil
}

func (c *Consumer) Push(ctx context.Context, jb *jobs.Job) error {
	const op = errors.Op("sqs_push")
	// check if the pipeline registered

	// load atomic value
	pipe := c.pipeline.Load().(*pipeline.Pipeline)
	if pipe.Name() != jb.Options.Pipeline {
		return errors.E(op, errors.Errorf("no such pipeline: %s, actual: %s", jb.Options.Pipeline, pipe.Name()))
	}

	// The length of time, in seconds, for which to delay a specific message. Valid
	// values: 0 to 900. Maximum: 15 minutes.
	if jb.Options.Delay > 900 {
		return errors.E(op, errors.Errorf("unable to push, maximum possible delay is 900 seconds (15 minutes), provided: %d", jb.Options.Delay))
	}

	err := c.handleItem(ctx, fromJob(jb))
	if err != nil {
		return errors.E(op, err)
	}

	return nil
}

func (c *Consumer) State(ctx context.Context) (*jobs.State, error) {
	const op = errors.Op("sqs_state")
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

	pipe := c.pipeline.Load().(*pipeline.Pipeline)

	out := &jobs.State{
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

func (c *Consumer) Register(_ context.Context, p *pipeline.Pipeline) error {
	c.pipeline.Store(p)
	return nil
}

func (c *Consumer) Run(_ context.Context, p *pipeline.Pipeline) error {
	start := time.Now()
	const op = errors.Op("sqs_run")

	c.Lock()
	defer c.Unlock()

	pipe := c.pipeline.Load().(*pipeline.Pipeline)
	if pipe.Name() != p.Name() {
		return errors.E(op, errors.Errorf("no such pipeline registered: %s", pipe.Name()))
	}

	atomic.AddUint32(&c.listeners, 1)

	// start listener
	// TODO(rustatian) context with cancel to cancel receive operation on stop
	go c.listen(context.Background())

	c.log.Debug("pipeline is active", zap.String("driver", pipe.Driver()), zap.String("pipeline", pipe.Name()), zap.Time("start", start), zap.Duration("elapsed", time.Since(start)))
	return nil
}

func (c *Consumer) Stop(context.Context) error {
	start := time.Now()
	if atomic.LoadUint32(&c.listeners) > 0 {
		c.pauseCh <- struct{}{}
	}

	pipe := c.pipeline.Load().(*pipeline.Pipeline)
	c.log.Debug("pipeline was stopped", zap.String("driver", pipe.Driver()), zap.String("pipeline", pipe.Name()), zap.Time("start", time.Now()), zap.Duration("elapsed", time.Since(start)))
	return nil
}

func (c *Consumer) Pause(_ context.Context, p string) {
	start := time.Now()
	// load atomic value
	pipe := c.pipeline.Load().(*pipeline.Pipeline)
	if pipe.Name() != p {
		c.log.Error("no such pipeline", zap.String("requested", p), zap.String("actual", pipe.Name()))
		return
	}

	l := atomic.LoadUint32(&c.listeners)
	// no active listeners
	if l == 0 {
		c.log.Warn("no active listeners, nothing to pause")
		return
	}

	atomic.AddUint32(&c.listeners, ^uint32(0))

	// stop consume
	c.pauseCh <- struct{}{}
	c.log.Debug("pipeline was paused", zap.String("driver", pipe.Driver()), zap.String("pipeline", pipe.Name()), zap.Time("start", time.Now()), zap.Duration("elapsed", time.Since(start)))
}

func (c *Consumer) Resume(_ context.Context, p string) {
	start := time.Now()
	// load atomic value
	pipe := c.pipeline.Load().(*pipeline.Pipeline)
	if pipe.Name() != p {
		c.log.Error("no such pipeline", zap.String("requested", p), zap.String("actual", pipe.Name()))
		return
	}

	l := atomic.LoadUint32(&c.listeners)
	// no active listeners
	if l == 1 {
		c.log.Warn("sqs listener already in the active state")
		return
	}

	// start listener
	go c.listen(context.Background())

	// increase num of listeners
	atomic.AddUint32(&c.listeners, 1)
	c.log.Debug("pipeline was resumed", zap.String("driver", pipe.Driver()), zap.String("pipeline", pipe.Name()), zap.Time("start", time.Now()), zap.Duration("elapsed", time.Since(start)))
}

func (c *Consumer) handleItem(ctx context.Context, msg *Item) error {
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

func checkEnv(insideAWS bool, key, secret, sessionToken, endpoint, region string) (*sqs.Client, error) {
	const op = errors.Op("check_env")
	var client *sqs.Client
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	switch insideAWS {
	case true:
		awsConf, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, errors.E(op, err)
		}

		// config with retries
		client = sqs.NewFromConfig(awsConf, func(o *sqs.Options) {
			o.Retryer = retry.NewStandard(func(opts *retry.StandardOptions) {
				opts.MaxAttempts = 60
			})
		})
	case false:
		awsConf, err := config.LoadDefaultConfig(ctx,
			config.WithRegion(region),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(key, secret, sessionToken)))
		if err != nil {
			return nil, errors.E(op, err)
		}
		// config with retries
		client = sqs.NewFromConfig(awsConf, sqs.WithEndpointResolver(sqs.EndpointResolverFromURL(endpoint)), func(o *sqs.Options) {
			o.Retryer = retry.NewStandard(func(opts *retry.StandardOptions) {
				opts.MaxAttempts = 60
			})
		})
	}

	return client, nil
}

func ready(r uint32) bool {
	return r > 0
}

// https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-instance-metadata.html
// https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/identify_ec2_instances.html
func isInAWS() bool {
	client := &http.Client{
		Timeout: time.Second * 2,
	}
	resp, err := client.Get(awsMetaDataURL) //nolint:noctx
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func createQueue(client *sqs.Client, queueName *string, attributes map[string]string, tags map[string]string) (*string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()
	out, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: queueName, Attributes: attributes, Tags: tags})
	if err != nil {
		if oErr, ok := (err).(*smithy.OperationError); ok { //nolint:errorlint
			if rErr, ok := oErr.Err.(*awshttp.ResponseError); ok { //nolint:errorlint
				if _, ok := rErr.Unwrap().(*types.QueueNameExists); ok { //nolint:errorlint
					ctxGet, cancelGet := context.WithTimeout(context.Background(), time.Second*30)
					defer cancelGet()
					res, errQ := client.GetQueueUrl(ctxGet, &sqs.GetQueueUrlInput{
						QueueName: queueName,
					}, func(_ *sqs.Options) {})
					if errQ != nil {
						return nil, errQ
					}

					return res.QueueUrl, nil
				}
			}
		}

		return nil, err
	}

	return out.QueueUrl, nil
}
