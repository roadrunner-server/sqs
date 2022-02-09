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
	for k := range attrs {
		switch k {
		case SqsManagedSseEnabled:
			ret[SqsManagedSseEnabledAWS] = attrs[k]
		case RedriveAllowPolicy:
			ret[RedriveAllowPolicyAWS] = attrs[k]
		case FifoThroughputLimit:
			/*
				FifoThroughputLimit – Specifies whether the FIFO queue throughput quota applies to the entire queue or per message group.
				Valid values are perQueue and perMessageGroupId. The perMessageGroupId value is allowed only when the value for DeduplicationScope is messageGroup.
			*/
			switch attrs[k] {
			case perQueue:
				ret[FifoThroughputLimitAWS] = perQueueAWS
			case perMessageGroupID:
				ret[FifoThroughputLimitAWS] = perMessageGroupIDAWS
			}
		case DeduplicationScope:
			// DeduplicationScope – Specifies whether message deduplication occurs at the
			// message group or queue level. Valid values are messageGroup and queue.
			if attrs[k] == messageGroup {
				ret[DeduplicationScopeAWS] = messageGroupAWS
			}
		case KmsDataKeyReusePeriodSeconds:
			ret[KmsDataKeyReusePeriodSecondsAWS] = attrs[k]
		case KmsMasterKeyID:
			ret[KmsMasterKeyIDAWS] = attrs[k]
		case ContentBasedDeduplication:
			ret[ContentBasedDeduplicationAWS] = attrs[k]
		case FifoQueue:
			ret[FifoQueueAWS] = attrs[k]
		case RedrivePolicy:
			ret[RedrivePolicyAWS] = attrs[k]
		case DelaySeconds:
			ret[DelaySecondsAWS] = attrs[k]
		case ApproximateNumberOfMessagesDelayed:
			ret[ApproximateNumberOfMessagesDelayedAWS] = attrs[k]
		case QueueArn:
			ret[QueueArnAWS] = attrs[k]
		case LastModifiedTimestamp:
			ret[LastModifiedTimestampAWS] = attrs[k]
		case Policy:
			ret[PolicyAWS] = attrs[k]
		case VisibilityTimeout:
			ret[VisibilityTimeoutAWS] = attrs[k]
		case MaximumMessageSize:
			ret[MaximumMessageSizeAWS] = attrs[k]
		case MessageRetentionPeriod:
			ret[MessageRetentionPeriodAWS] = attrs[k]
		case ApproximateNumberOfMessages:
			ret[ApproximateNumberOfMessagesAWS] = attrs[k]
		case ApproximateNumberOfMessagesNotVisible:
			ret[ApproximateNumberOfMessagesNotVisibleAWS] = attrs[k]
		case CreatedTimestamp:
			ret[CreatedTimestampAWS] = attrs[k]
		case ReceiveMessageWaitTimeSeconds:
			ret[ReceiveMessageWaitTimeSecondsAWS] = attrs[k]
		default:
			continue
		}
	}
}
