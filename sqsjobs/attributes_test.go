package sqsjobs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAttributes(t *testing.T) {
	m := make(map[string]string)
	m["policy"] = "foo" //nolint:goconst
	m["visibilitytimeout"] = "foo"
	m["maximummessagesize"] = "foo"
	m["messageretentionperiod"] = "foo"
	m["approximatenumberofmessages"] = "foo"
	m["approximatenumberofmessagesnotvisible"] = "foo"
	m["createdtimestamp"] = "foo"
	m["lastmodifiedtimestamp"] = "foo"
	m["queuearn"] = "foo"
	m["approximatenumberofmessagesdelayed"] = "foo"
	m["delayseconds"] = "foo"
	m["redrivepolicy"] = "foo"
	m["fifoqueue"] = "foo"
	m["contentbaseddeduplication"] = "foo"
	m["kmsmasterkeyid"] = "foo"
	m["kmsdatakeyreuseperiodseconds"] = "foo"
	m["fifothroughputlimit"] = "foo"
	m["redriveallowpolicy"] = "foo"
	m["sqsmanagedsseenabled"] = "foo"
	m["receivemessagewaittimeseconds"] = "foo"

	m["deduplicationscope"] = "messagegroup"
	m["fifothroughputlimit"] = "perqueue"

	m2 := make(map[string]string)
	toAwsAttribute(m, m2)
	cfg := Config{Attributes: m}
	cfg.InitDefault()

	require.Equal(t, "perQueue", m2["FifoThroughputLimit"])
	require.Equal(t, "messageGroup", m2["DeduplicationScope"])
	require.Equal(t, "foo", m2["Policy"])
	require.Equal(t, "foo", m2["VisibilityTimeout"])
	require.Equal(t, "foo", m2["MaximumMessageSize"])
	require.Equal(t, "foo", m2["MessageRetentionPeriod"])
	require.Equal(t, "foo", m2["ApproximateNumberOfMessages"])
	require.Equal(t, "foo", m2["ApproximateNumberOfMessagesNotVisible"])
	require.Equal(t, "foo", m2["CreatedTimestamp"])
	require.Equal(t, "foo", m2["LastModifiedTimestamp"])
	require.Equal(t, "foo", m2["QueueArn"])
	require.Equal(t, "foo", m2["ApproximateNumberOfMessagesDelayed"])
	require.Equal(t, "foo", m2["RedrivePolicy"])
	require.Equal(t, "foo", m2["FifoQueue"])
	require.Equal(t, "foo", m2["ContentBasedDeduplication"])
	require.Equal(t, "foo", m2["KmsMasterKeyId"])
	require.Equal(t, "foo", m2["KmsDataKeyReusePeriodSeconds"])
	require.Equal(t, "foo", m2["RedriveAllowPolicy"])
	require.Equal(t, "foo", m2["SqsManagedSseEnabled"])
	require.Equal(t, "foo", m2["ReceiveMessageWaitTimeSeconds"])
}

func TestAttributes2(t *testing.T) {
	m := make(map[string]string)
	m["policy"] = "foo"
	m["visibilitytimeout"] = "foo"
	m["maximummessagesize"] = "foo"
	m["messageretentionperiod"] = "foo"
	m["approximatenumberofmessages"] = "foo"
	m["approximatenumberofmessagesnotvisible"] = "foo"
	m["createdtimestamp"] = "foo"
	m["lastmodifiedtimestamp"] = "foo"
	m["queuearn"] = "foo"
	m["approximatenumberofmessagesdelayed"] = "foo"
	m["delayseconds"] = "foo"
	m["redrivepolicy"] = "foo"
	m["fifoqueue"] = "foo"
	m["contentbaseddeduplication"] = "foo"
	m["kmsmasterkeyid"] = "foo"
	m["kmsdatakeyreuseperiodseconds"] = "foo"
	m["fifothroughputlimit"] = "foo"
	m["redriveallowpolicy"] = "foo"
	m["sqsmanagedsseenabled"] = "foo"
	m["receivemessagewaittimeseconds"] = "foo"

	m["deduplicationscope"] = "messagegroup"
	m["fifothroughputlimit"] = "permessagegroupid"

	m2 := make(map[string]string)
	toAwsAttribute(m, m2)
	cfg := Config{Attributes: m}
	cfg.InitDefault()

	require.Equal(t, "perMessageGroupId", m2["FifoThroughputLimit"])
	require.Equal(t, "messageGroup", m2["DeduplicationScope"])
	require.Equal(t, "foo", m2["Policy"])
	require.Equal(t, "foo", m2["VisibilityTimeout"])
	require.Equal(t, "foo", m2["MaximumMessageSize"])
	require.Equal(t, "foo", m2["MessageRetentionPeriod"])
	require.Equal(t, "foo", m2["ApproximateNumberOfMessages"])
	require.Equal(t, "foo", m2["ApproximateNumberOfMessagesNotVisible"])
	require.Equal(t, "foo", m2["CreatedTimestamp"])
	require.Equal(t, "foo", m2["LastModifiedTimestamp"])
	require.Equal(t, "foo", m2["QueueArn"])
	require.Equal(t, "foo", m2["ApproximateNumberOfMessagesDelayed"])
	require.Equal(t, "foo", m2["RedrivePolicy"])
	require.Equal(t, "foo", m2["FifoQueue"])
	require.Equal(t, "foo", m2["ContentBasedDeduplication"])
	require.Equal(t, "foo", m2["KmsMasterKeyId"])
	require.Equal(t, "foo", m2["KmsDataKeyReusePeriodSeconds"])
	require.Equal(t, "foo", m2["RedriveAllowPolicy"])
	require.Equal(t, "foo", m2["SqsManagedSseEnabled"])
	require.Equal(t, "foo", m2["ReceiveMessageWaitTimeSeconds"])
}
