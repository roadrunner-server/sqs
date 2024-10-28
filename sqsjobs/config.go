package sqsjobs

import (
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
)

const (
	attributes             string = "attributes"
	tags                   string = "tags"
	queue                  string = "queue"
	pref                   string = "prefetch"
	visibility             string = "visibility_timeout"
	errorVisibilityTimeout string = "error_visibility_timeout"
	retainFailedJobs       string = "retain_failed_jobs"
	prefetch               string = "prefetch"
	messageGroupID         string = "message_group_id"
	waitTime               string = "wait_time"
	skipQueueDeclaration   string = "skip_queue_declaration"
	maxVisibilityTimeout   int32  = 43200
	maxWaitTime            int32  = 20
)

// Config is used to parse pipeline configuration
type Config struct {
	// global
	Key          string `mapstructure:"key"`
	Secret       string `mapstructure:"secret"`
	Region       string `mapstructure:"region"`
	SessionToken string `mapstructure:"session_token"`
	Endpoint     string `mapstructure:"endpoint"`

	// pipeline

	// get queue url, do not declare
	SkipQueueDeclaration bool `mapstructure:"skip_queue_declaration"`

	// The duration (in seconds) that the received messages are hidden from subsequent
	// retrieve requests after being retrieved by a ReceiveMessage request.
	VisibilityTimeout int32 `mapstructure:"visibility_timeout"`

	// If defined (> 0) and RetainFailedJobs is true, RR will change the visibility timeout of failed jobs and let them
	// be received again, instead of deleting and re-queueing them as new jobs. This allows you to use the automatic SQS
	// dead-letter feature by setting a maximum receive count on your queue. This produces similar behavior to Elastic
	// Beanstalk's worker environments.
	// If this is enabled, your driver credentials must have the sqs:ChangeMessageVisibility permission for the queue.
	ErrorVisibilityTimeout int32 `mapstructure:"error_visibility_timeout"`

	// Whether to retain failed jobs in the queue. If you set this to true, jobs will be consumed by the
	// workers again after VisibilityTimeout, or ErrorVisibilityTimeout (if set), has passed.
	// If this is false, jobs will be deleted from the queue and immediately queued again as new jobs.
	// Defaults to false.
	RetainFailedJobs bool `mapstructure:"retain_failed_jobs"`

	// The duration (in seconds) for which the call waits for a message to arrive
	// in the queue before returning. If a message is available, the call returns
	// sooner than WaitTimeSeconds. If no messages are available and the wait time
	// expires, the call returns successfully with an empty list of messages.
	WaitTimeSeconds int32 `mapstructure:"wait_time_seconds"`

	// Prefetch is the maximum number of messages to return. Amazon SQS never returns more messages
	// than this value (however, fewer messages might be returned). Valid values: 1 to
	// 10. Default: 1.
	Prefetch int32 `mapstructure:"prefetch"`

	// The name of the new queue. The following limits apply to this name:
	//
	// * A queue
	// name can have up to 80 characters.
	//
	// * Valid values: alphanumeric characters,
	// hyphens (-), and underscores (_).
	//
	// * A FIFO queue name must end with the .fifo
	// suffix.
	//
	// Queue URLs and names are case-sensitive.
	//
	// This member is required.
	Queue *string `mapstructure:"queue"`

	/*
		link: https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_SendMessage.html
		This parameter applies only to FIFO (first-in-first-out) queues.
		The tag that specifies that a message belongs to a specific message group. Messages that belong to the same message group are processed in a FIFO manner
		(however, messages in different message groups might be processed out of order).
		To interleave multiple ordered streams within a single queue, use MessageGroupId values (for example, session data for multiple users).
		In this scenario, multiple consumers can process the queue, but the session data of each user is processed in a FIFO fashion.
		You must associate a non-empty MessageGroupId with a message. If you don't provide a MessageGroupId, the action fails.
		ReceiveMessage might return messages with multiple MessageGroupId values. For each MessageGroupId, the messages are sorted by time sent. The caller can't specify a MessageGroupId.
		The length of MessageGroupId is 128 characters. Valid values: alphanumeric characters and punctuation (!"#$%&'()*+,-./:;<=>?@[\]^_`{|}~).
	*/
	MessageGroupID string `mapstructure:"message_group_id"`

	// A map of attributes with their corresponding values. The following lists the
	// names, descriptions, and values of the special request parameters that the
	// CreateQueue action uses.
	// https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_SetQueueAttributes.html
	Attributes map[string]string `mapstructure:"attributes"`

	// From amazon docs:
	// Add cost allocation tags to the specified Amazon SQS queue. For an overview, see
	// Tagging Your Amazon SQS Queues
	// (https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-queue-tags.html)
	// in the Amazon SQS Developer Guide. When you use queue tags, keep the following
	// guidelines in mind:
	//
	// * Adding more than 50 tags to a queue isn't recommended.
	//
	// *
	// Tags don't have any semantic meaning. Amazon SQS interprets tags as character
	// strings.
	//
	// * Tags are case-sensitive.
	//
	// * A new tag with a key identical to that
	// of an existing tag overwrites the existing tag.
	//
	// For a full list of tag
	// restrictions, see Quotas related to queues
	// (https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-limits.html#limits-queues)
	// in the Amazon SQS Developer Guide. To be able to tag a queue on creation, you
	// must have the sqs:CreateQueue and sqs:TagQueue permissions. Cross-account
	// permissions don't apply to this action. For more information, see Grant
	// cross-account permissions to a role and a user name
	// (https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-customer-managed-policy-examples.html#grant-cross-account-permissions-to-role-and-user-name)
	// in the Amazon SQS Developer Guide.
	Tags map[string]string `mapstructure:"tags"`
}

func (c *Config) InitDefault() {
	if c.Queue == nil {
		c.Queue = aws.String("default")
	}

	if c.Prefetch <= 0 || c.Prefetch > 10 {
		c.Prefetch = 10
	}

	if c.WaitTimeSeconds < 0 {
		// 0 - ignored by AWS
		c.WaitTimeSeconds = 0
	} else if c.WaitTimeSeconds > maxWaitTime {
		c.WaitTimeSeconds = maxWaitTime
	}

	// Make sure visibility timeouts are within the allowed boundaries.
	if c.VisibilityTimeout < 0 {
		// 0 - ignored by AWS
		c.VisibilityTimeout = 0
	} else if c.VisibilityTimeout > maxVisibilityTimeout {
		c.VisibilityTimeout = maxVisibilityTimeout
	}

	if c.ErrorVisibilityTimeout < 0 {
		c.ErrorVisibilityTimeout = 0
	} else if c.ErrorVisibilityTimeout > maxVisibilityTimeout {
		c.ErrorVisibilityTimeout = maxVisibilityTimeout
	}

	if c.Attributes != nil {
		newAttr := make(map[string]string, len(c.Attributes))
		toAwsAttribute(c.Attributes, newAttr)
		// clear old map
		for k := range c.Attributes {
			delete(c.Attributes, k)
		}

		c.Attributes = newAttr
	} else {
		c.Attributes = make(map[string]string)
	}

	if c.Tags == nil {
		c.Tags = make(map[string]string)
	}

	// used for the tests
	if str := os.Getenv("RR_TEST_ENV"); str != "" {
		// All parameters are required for the tests to succeed, so we
		// fail fast here if this is not configured correctly.
		if os.Getenv("RR_SQS_TEST_REGION") == "" ||
			os.Getenv("RR_SQS_TEST_KEY") == "" ||
			os.Getenv("RR_SQS_TEST_SECRET") == "" ||
			os.Getenv("RR_SQS_TEST_ENDPOINT") == "" ||
			os.Getenv("RR_SQS_TEST_ACCOUNT_ID") == "" {
			panic("security check: test mode is enabled, but not all sqs environment parameters are set")
		}
		c.Region = os.Getenv("RR_SQS_TEST_REGION")
		c.Key = os.Getenv("RR_SQS_TEST_KEY")
		c.Secret = os.Getenv("RR_SQS_TEST_SECRET")
		c.Endpoint = os.Getenv("RR_SQS_TEST_ENDPOINT")
	}
}
