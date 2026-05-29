package sqsjobs

import (
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/roadrunner-server/api-plugins/v6/jobs"
)

func convAttr(h map[string]string) map[string][]string {
	ret := make(map[string][]string, len(h))

	for k, v := range h {
		ret[k] = []string{v}
	}

	return ret
}

// rrKeys is the set of RoadRunner-owned MessageAttribute keys excluded from header copy.
var rrKeys = map[string]struct{}{ //nolint:gochecknoglobals
	jobs.RRJob:      {},
	jobs.RRID:       {},
	jobs.RRDelay:    {},
	jobs.RRAutoAck:  {},
	jobs.RRPriority: {},
	jobs.RRPipeline: {},
	jobs.RRHeaders:  {},
}

func convMessageAttr(h map[string]types.MessageAttributeValue, curr *map[string][]string) {
	for k, v := range h {
		if _, ok := rrKeys[k]; ok {
			continue
		}

		if v.DataType == nil {
			continue
		}

		// Amazon SQS supports the following logical data types: String, Number, and Binary .
		switch *v.DataType {
		case BinaryType:
			if v.BinaryValue == nil {
				continue
			}

			(*curr)[k] = []string{string(v.BinaryValue)}
		case StringType:
			if v.StringValue == nil {
				continue
			}

			(*curr)[k] = []string{*v.StringValue}
		case NumberType:
			if len(v.BinaryValue) != 0 {
				(*curr)[k] = []string{string(v.BinaryValue)}
				continue
			}

			if v.StringValue != nil {
				(*curr)[k] = []string{*v.StringValue}
			}
		}
	}
}
