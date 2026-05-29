package sqsjobs

const (
	Policy                                   string = "policy"
	PolicyAWS                                string = "Policy"
	VisibilityTimeout                        string = "visibilitytimeout"
	VisibilityTimeoutAWS                     string = "VisibilityTimeout"
	MaximumMessageSize                       string = "maximummessagesize"
	MaximumMessageSizeAWS                    string = "MaximumMessageSize"
	MessageRetentionPeriod                   string = "messageretentionperiod"
	MessageRetentionPeriodAWS                string = "MessageRetentionPeriod"
	ApproximateNumberOfMessages              string = "approximatenumberofmessages"
	ApproximateNumberOfMessagesAWS           string = "ApproximateNumberOfMessages"
	ApproximateNumberOfMessagesNotVisible    string = "approximatenumberofmessagesnotvisible"
	ApproximateNumberOfMessagesNotVisibleAWS string = "ApproximateNumberOfMessagesNotVisible"
	CreatedTimestamp                         string = "createdtimestamp"
	CreatedTimestampAWS                      string = "CreatedTimestamp"
	LastModifiedTimestamp                    string = "lastmodifiedtimestamp"
	LastModifiedTimestampAWS                 string = "LastModifiedTimestamp"
	QueueArn                                 string = "queuearn"
	QueueArnAWS                              string = "QueueArn"
	ApproximateNumberOfMessagesDelayed       string = "approximatenumberofmessagesdelayed"
	ApproximateNumberOfMessagesDelayedAWS    string = "ApproximateNumberOfMessagesDelayed"
	DelaySeconds                             string = "delayseconds"
	DelaySecondsAWS                          string = "DelaySeconds"
	RedrivePolicy                            string = "redrivepolicy"
	RedrivePolicyAWS                         string = "RedrivePolicy"
	FifoQueue                                string = "fifoqueue"
	FifoQueueAWS                             string = "FifoQueue"
	ContentBasedDeduplication                string = "contentbaseddeduplication"
	ContentBasedDeduplicationAWS             string = "ContentBasedDeduplication"
	KmsMasterKeyID                           string = "kmsmasterkeyid"
	KmsMasterKeyIDAWS                        string = "KmsMasterKeyId"
	KmsDataKeyReusePeriodSeconds             string = "kmsdatakeyreuseperiodseconds"
	KmsDataKeyReusePeriodSecondsAWS          string = "KmsDataKeyReusePeriodSeconds"
	DeduplicationScope                       string = "deduplicationscope"
	DeduplicationScopeAWS                    string = "DeduplicationScope"
	FifoThroughputLimit                      string = "fifothroughputlimit"
	FifoThroughputLimitAWS                   string = "FifoThroughputLimit"
	RedriveAllowPolicy                       string = "redriveallowpolicy"
	RedriveAllowPolicyAWS                    string = "RedriveAllowPolicy"
	SqsManagedSseEnabled                     string = "sqsmanagedsseenabled"
	SqsManagedSseEnabledAWS                  string = "SqsManagedSseEnabled"
	ReceiveMessageWaitTimeSeconds            string = "receivemessagewaittimeseconds"
	ReceiveMessageWaitTimeSecondsAWS         string = "ReceiveMessageWaitTimeSeconds"
)

// Attr.Value
const (
	messageGroup    string = "messagegroup"
	messageGroupAWS string = "messageGroup"

	perQueue    string = "perqueue"
	perQueueAWS string = "perQueue"

	perMessageGroupID    string = "permessagegroupid"
	perMessageGroupIDAWS string = "perMessageGroupId"
)

// toAwsAttribute maps attributes to its AWS values
func toAwsAttribute(attrs map[string]string, ret map[string]string) { //nolint:gocyclo
	for k, v := range attrs {
		switch k {
		case SqsManagedSseEnabled:
			ret[SqsManagedSseEnabledAWS] = v
		case RedriveAllowPolicy:
			ret[RedriveAllowPolicyAWS] = v
		case FifoThroughputLimit:
			/*
				FifoThroughputLimit – Specifies whether the FIFO queue throughput quota applies to the entire queue or per message group.
				Valid values are perQueue and perMessageGroupId. The perMessageGroupId value is allowed only when the value for DeduplicationScope is messageGroup.
			*/
			switch v {
			case perQueue:
				ret[FifoThroughputLimitAWS] = perQueueAWS
			case perMessageGroupID:
				ret[FifoThroughputLimitAWS] = perMessageGroupIDAWS
			}
		case DeduplicationScope:
			// DeduplicationScope – Specifies whether message deduplication occurs at the
			// message group or queue level. Valid values are messageGroup and queue.
			if v == messageGroup {
				ret[DeduplicationScopeAWS] = messageGroupAWS
			}
		case KmsDataKeyReusePeriodSeconds:
			ret[KmsDataKeyReusePeriodSecondsAWS] = v
		case KmsMasterKeyID:
			ret[KmsMasterKeyIDAWS] = v
		case ContentBasedDeduplication:
			ret[ContentBasedDeduplicationAWS] = v
		case FifoQueue:
			ret[FifoQueueAWS] = v
		case RedrivePolicy:
			ret[RedrivePolicyAWS] = v
		case DelaySeconds:
			ret[DelaySecondsAWS] = v
		case ApproximateNumberOfMessagesDelayed:
			ret[ApproximateNumberOfMessagesDelayedAWS] = v
		case QueueArn:
			ret[QueueArnAWS] = v
		case LastModifiedTimestamp:
			ret[LastModifiedTimestampAWS] = v
		case Policy:
			ret[PolicyAWS] = v
		case VisibilityTimeout:
			ret[VisibilityTimeoutAWS] = v
		case MaximumMessageSize:
			ret[MaximumMessageSizeAWS] = v
		case MessageRetentionPeriod:
			ret[MessageRetentionPeriodAWS] = v
		case ApproximateNumberOfMessages:
			ret[ApproximateNumberOfMessagesAWS] = v
		case ApproximateNumberOfMessagesNotVisible:
			ret[ApproximateNumberOfMessagesNotVisibleAWS] = v
		case CreatedTimestamp:
			ret[CreatedTimestampAWS] = v
		case ReceiveMessageWaitTimeSeconds:
			ret[ReceiveMessageWaitTimeSecondsAWS] = v
		default:
			continue
		}
	}
}
