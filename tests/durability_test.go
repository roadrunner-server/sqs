package tests

import (
	"testing"

	"tests/helpers"
)

const (
	durabilityAddr = "127.0.0.1:6001"
	// proxyName fronts the queue endpoint on 19324, which the durability config
	// dials. Both addresses are inside the compose network; 19324 is published.
	proxyName     = "redial"
	proxyListen   = "0.0.0.0:19324"
	proxyUpstream = "localstack:4566"
)

// TestRedialAfterOutage cuts the connection to the endpoint underneath a
// running pipeline and checks the driver recovers once it comes back. The old
// test made the same calls behind 23 seconds of sleeps and asserted nothing.
func TestRedialAfterOutage(t *testing.T) {
	t.Cleanup(func() { helpers.DeleteQueues(t, "sqs-durability-1", "sqs-durability-2") })

	helpers.CreateProxy(t, proxyName, proxyListen, proxyUpstream)

	rr, _ := helpers.Start(t, "configs/.rr-sqs-durability-redial.yaml", jobsPlugins(),
		helpers.WithObservedLogger(),
		helpers.WithTCPProbe(durabilityAddr),
	)

	rr.RequireLogCount(t, "pipeline was started", 2)

	helpers.PushToPipe("test-1", false, durabilityAddr)(t)
	helpers.PushToPipe("test-2", false, durabilityAddr)(t)
	rr.WaitLog(t, "job was processed successfully", 2)

	helpers.SetProxyEnabled(t, proxyName, false)
	helpers.SetProxyEnabled(t, proxyName, true)

	// the sdk has to get through again before these land
	helpers.PushEventually(t, durabilityAddr, "test-1")
	helpers.PushEventually(t, durabilityAddr, "test-2")

	rr.WaitLog(t, "job was processed successfully", 4)

	helpers.DestroyPipelines(durabilityAddr, "test-1", "test-2")(t)

	rr.RequireLogCount(t, "pipeline was stopped", 2)
}
