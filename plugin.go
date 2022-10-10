package sqs

import (
	"github.com/roadrunner-server/sdk/v3/plugins/jobs"
	"github.com/roadrunner-server/sdk/v3/plugins/jobs/pipeline"
	priorityqueue "github.com/roadrunner-server/sdk/v3/priority_queue"
	"github.com/roadrunner-server/sqs/v3/sqsjobs"
	"go.uber.org/zap"
)

const (
	pluginName string = "sqs"
)

type Plugin struct {
	log *zap.Logger
	cfg Configurer
}

type Configurer interface {
	// UnmarshalKey takes a single key and unmarshal it into a Struct.
	UnmarshalKey(name string, out any) error

	// Has checks if config section exists.
	Has(name string) bool
}

func (p *Plugin) Init(log *zap.Logger, cfg Configurer) error {
	p.log = new(zap.Logger)
	*p.log = *log
	p.cfg = cfg
	return nil
}

func (p *Plugin) Name() string {
	return pluginName
}

func (p *Plugin) ConsumerFromConfig(configKey string, pq priorityqueue.Queue) (jobs.Consumer, error) {
	return sqsjobs.NewSQSConsumer(configKey, p.log, p.cfg, pq)
}

func (p *Plugin) ConsumerFromPipeline(pipe *pipeline.Pipeline, pq priorityqueue.Queue) (jobs.Consumer, error) {
	return sqsjobs.FromPipeline(pipe, p.log, p.cfg, pq)
}
