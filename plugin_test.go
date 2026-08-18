package sqs

import (
	"log/slog"
	"os"
	"testing"

	"github.com/roadrunner-server/errors"
	"github.com/stretchr/testify/require"
)

type testLogger struct{}

func (*testLogger) NamedLogger(string) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// testCfg reports the listed sections as present and nothing else.
type testCfg struct {
	sections map[string]struct{}
}

func newCfg(sections ...string) *testCfg {
	c := &testCfg{sections: make(map[string]struct{}, len(sections))}
	for _, s := range sections {
		c.sections[s] = struct{}{}
	}

	return c
}

func (*testCfg) UnmarshalKey(string, any) error { return nil }

func (c *testCfg) Has(name string) bool {
	_, ok := c.sections[name]
	return ok
}

func TestPluginName(t *testing.T) {
	require.Equal(t, "sqs", (&Plugin{}).Name())
}

// TestPluginInit covers the enable rule: either section on its own is enough,
// and the plugin only disables itself when neither is configured.
func TestPluginInit(t *testing.T) {
	require.NoError(t, (&Plugin{}).Init(&testLogger{}, newCfg("sqs")))
	require.NoError(t, (&Plugin{}).Init(&testLogger{}, newCfg("jobs")))

	err := (&Plugin{}).Init(&testLogger{}, newCfg("http"))

	require.Error(t, err)
	require.True(t, errors.Is(errors.Disabled, err), "expected a Disabled error, got %v", err)
}

func TestCollectsTracer(t *testing.T) {
	require.Len(t, (&Plugin{}).Collects(), 1)
}
