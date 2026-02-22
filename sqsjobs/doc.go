// Package sqsjobs implements the core AWS SQS jobs driver for RoadRunner.
//
// The central type is [Driver], which satisfies the jobs.Driver interface and
// manages the full message lifecycle: pushing jobs to an SQS queue, consuming
// them via long-polling, and supporting pause/resume of individual pipelines.
// Queue creation is handled automatically unless SkipQueueDeclaration is set.
//
// [Item] represents a single job and carries its payload, headers, and
// execution options such as delay and auto-acknowledge. Messages are
// serialized into SQS message attributes for round-trip fidelity, including
// FIFO message-group support.
//
// Configuration is captured in [Config] and covers AWS credentials, queue
// attributes, prefetch depth, visibility timeouts, and in-flight message
// limits.
//
// All driver operations emit OpenTelemetry spans and propagate trace context
// through SQS message attributes using the W3C TraceContext, Baggage, and
// Jaeger propagators.
package sqsjobs
