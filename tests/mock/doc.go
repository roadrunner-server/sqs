// Package mocklogger provides mock logging infrastructure for tests.
//
// It supplies [ZapLoggerMock], an Endure-compatible plugin that produces a zap
// logger backed by an in-memory observer. Tests use [ObservedLogs] to capture
// and assert on log entries by level, message, or field without writing to
// disk.
package mocklogger
