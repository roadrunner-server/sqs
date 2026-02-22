// Package sqs provides a RoadRunner jobs driver for AWS SQS queues.
//
// The plugin registers itself under the "sqs" name and implements the Endure
// service container lifecycle (Init, Collects). It reads connection credentials
// and queue settings from the application configuration and produces
// [jobs.Driver] instances via DriverFromConfig and DriverFromPipeline.
//
// OpenTelemetry tracing is supported: when a TracerProvider is collected
// through dependency injection, every driver created by the plugin
// propagates trace context across SQS messages.
package sqs
