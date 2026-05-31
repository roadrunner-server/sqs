package sqsjobs

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"
	"go.opentelemetry.io/otel/propagation"
)

const (
	// All - get all message attribute names
	All string = "All"
	// NonExistentQueue AWS error code
	NonExistentQueue string = "AWS.SimpleQueueService.NonExistentQueue"
	// consume all
	auto string = "deduced_by_rr"
)

func (c *Driver) listen(ctx context.Context) { //nolint:gocognit
	go func() {
		for {
			select {
			case <-c.pauseCh:
				c.log.Debug("sqs listener was stopped")
				return
			default:
				message, err := c.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
					QueueUrl:                    c.queueURL,
					MaxNumberOfMessages:         c.prefetch,
					MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeName(All)},
					MessageAttributeNames:       []string{All},
					VisibilityTimeout:           c.visibilityTimeout,
					WaitTimeSeconds:             c.waitTime,
				})

				if err != nil { //nolint:nestif
					if c.skipDeclare {
						c.log.Error("receive message", "error", err)
						continue
					}

					var oErr *smithy.OperationError
					if !errors.As(err, &oErr) {
						c.log.Error("receive message", "error", err)
						continue
					}

					var rErr *http.ResponseError
					if !errors.As(oErr.Err, &rErr) {
						c.log.Error("receive message", "error", err)
						continue
					}

					var apiErr *smithy.GenericAPIError
					if !errors.As(rErr.Err, &apiErr) {
						c.log.Error("receive message", "error", err)
						continue
					}

					if apiErr.Code != NonExistentQueue {
						c.log.Error("receive message", "error", err)
						continue
					}

					c.log.Error("receive message", "error code", apiErr.ErrorCode(), "message", apiErr.ErrorMessage(), "error fault", apiErr.ErrorFault().String())
					_, err = c.client.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: c.queue, Attributes: c.attributes, Tags: c.tags})
					if err != nil {
						c.log.Error("create queue", "error", err)
					}

					// To successfully create a new queue, you must provide a
					// queue name that adheres to the limits related to the queues
					// (https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/limits-queues.html)
					// and is unique within the scope of your queues. After you create a queue, you
					// must wait at least one second after the queue is created to be able to use the <------------
					// queue. To get the queue URL, use the GetQueueUrl action. GetQueueUrl require
					time.Sleep(time.Second)
					continue
				}

				for i := range message.Messages {
					c.cond.L.Lock()
					// lock when we hit the limit
					for c.msgInFlight.Load() >= int64(c.msgInFlightLimit.Load()) {
						c.log.Debug("prefetch limit was reached, waiting for the jobs to be processed", "current", c.msgInFlight.Load(), "limit", c.msgInFlightLimit.Load())
						c.cond.Wait()
					}

					m := message.Messages[i]
					c.log.Debug("receive message", "ID", m.MessageId)
					item := c.unpack(&m)

					ctxspan, span := c.tracer.Tracer(tracerName).Start(c.prop.Extract(ctx, propagation.HeaderCarrier(item.headers)), "sqs_listener")

					if item.Options.AutoAck {
						ctxT, cancel := context.WithTimeout(ctx, time.Minute)
						_, errD := c.client.DeleteMessage(ctxT, &sqs.DeleteMessageInput{
							QueueUrl:      c.queueURL,
							ReceiptHandle: m.ReceiptHandle,
						})
						if errD != nil {
							cancel()
							c.log.Error("message unpack, failed to delete the message from the queue", "error", errD)
							c.cond.L.Unlock()

							span.RecordError(errD)
							span.End()
							continue
						}
						cancel()

						c.log.Debug("auto ack is turned on, message acknowledged")
					}

					if item.headers == nil {
						item.headers = make(map[string][]string, 2)
					}

					// copy all system attributes to the headers which would be used in the PHP Headers
					for k, v := range m.Attributes {
						item.headers[k] = []string{v}
					}

					c.prop.Inject(ctxspan, propagation.HeaderCarrier(item.headers))

					c.pq.Insert(item)
					// increase the current number of messages
					c.msgInFlight.Add(1)
					c.log.Debug("message pushed to the priority queue", "current", c.msgInFlight.Load(), "limit", c.msgInFlightLimit.Load())
					c.cond.L.Unlock()
					span.End()
				}
			}
		}
	}()
}
