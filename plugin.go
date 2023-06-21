package sqs

import (
	"github.com/roadrunner-server/api/v4/plugins/v2/jobs"
	"github.com/roadrunner-server/endure/v2/dep"
	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/sqs/v4/sqsjobs"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
)

const (
	pluginName       string = "sqs"
	masterPluginName string = "jobs"
)

type Plugin struct {
	tracer *sdktrace.TracerProvider

	log *zap.Logger
	cfg Configurer
}

type Configurer interface {
	// UnmarshalKey takes a single key and unmarshal it into a Struct.
	UnmarshalKey(name string, out any) error
	// Has checks if config section exists.
	Has(name string) bool
}

type Tracer interface {
	Tracer() *sdktrace.TracerProvider
}

type Logger interface {
	NamedLogger(name string) *zap.Logger
}

func (p *Plugin) Init(log Logger, cfg Configurer) error {
	// if there is no sqs section and no job section -> disable
	if !cfg.Has(pluginName) && !cfg.Has(masterPluginName) {
		return errors.E(errors.Disabled)
	}

	p.log = log.NamedLogger(pluginName)
	p.cfg = cfg
	return nil
}

func (p *Plugin) Name() string {
	return pluginName
}

func (p *Plugin) Collects() []*dep.In {
	return []*dep.In{
		dep.Fits(func(pp any) {
			p.tracer = pp.(Tracer).Tracer()
		}, (*Tracer)(nil)),
	}
}

func (p *Plugin) DriverFromConfig(configKey string, pq jobs.Queue, pipeline jobs.Pipeline, _ chan<- jobs.Commander) (jobs.Driver, error) {
	return sqsjobs.FromConfig(p.tracer, configKey, pipeline, p.log, p.cfg, pq)
}

func (p *Plugin) DriverFromPipeline(pipe jobs.Pipeline, pq jobs.Queue, _ chan<- jobs.Commander) (jobs.Driver, error) {
	return sqsjobs.FromPipeline(p.tracer, pipe, p.log, p.cfg, pq)
}
