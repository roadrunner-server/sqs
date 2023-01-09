package sqs

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/roadrunner-server/api/v3/plugins/v1/jobs"
	pq "github.com/roadrunner-server/api/v3/plugins/v1/priority_queue"
	"github.com/roadrunner-server/sqs/v4/sqsjobs"
	"go.uber.org/zap"
)

const (
	pluginName           string = "sqs"
	awsMetaDataURL       string = "http://169.254.169.254/latest/dynamic/instance-identity/"
	awsMetaDataIMDSv2URL string = "http://169.254.169.254/latest/api/token"
	awsTokenHeader       string = "X-aws-ec2-metadata-token-ttl-seconds" //nolint:gosec
)

type Plugin struct {
	insideAWS bool
	mu        sync.RWMutex

	log *zap.Logger
	cfg Configurer
}

type Configurer interface {
	// UnmarshalKey takes a single key and unmarshal it into a Struct.
	UnmarshalKey(name string, out any) error
	// Has checks if config section exists.
	Has(name string) bool
}

type Logger interface {
	NamedLogger(name string) *zap.Logger
}

func (p *Plugin) Init(log Logger, cfg Configurer) error {
	p.log = log.NamedLogger(pluginName)
	p.cfg = cfg

	/*
		we need to determine in what environment we are running
		1. Non-AWS - global sqs config should be set
		2. AWS - configuration should be obtained from the env
	*/
	go func() {
		p.mu.Lock()
		p.insideAWS = isInAWS() || isinAWSIMDSv2()
		p.mu.Unlock()
	}()
	return nil
}

func (p *Plugin) Name() string {
	return pluginName
}

func (p *Plugin) ConsumerFromConfig(configKey string, pq pq.Queue) (jobs.Consumer, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return sqsjobs.NewSQSConsumer(configKey, p.insideAWS, p.log, p.cfg, pq)
}

func (p *Plugin) ConsumerFromPipeline(pipe jobs.Pipeline, pq pq.Queue) (jobs.Consumer, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return sqsjobs.FromPipeline(pipe, p.insideAWS, p.log, p.cfg, pq)
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

// https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/configuring-instance-metadata-service.html
func isinAWSIMDSv2() bool {
	client := &http.Client{
		Timeout: time.Second * 2,
	}

	// probably we're in the IMDSv2, let's try different endpoint
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, awsMetaDataIMDSv2URL, nil)
	if err != nil {
		return false
	}

	// 10 seconds should be fine to just check
	req.Header.Set(awsTokenHeader, "10")

	resp, err := client.Do(req)
	if err != nil {
		return false
	}

	_ = resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}
