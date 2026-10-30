package mail

import (
	"context"
	"fmt"

	"github.com/golaravel/golaravel/pkg/queue"
)

// SendQueuedMailJob encapsulates an email message for asynchronous queue execution.
type SendQueuedMailJob struct {
	Message *Message `json:"message"`
}

func init() {
	queue.RegisterJobType(&SendQueuedMailJob{})
}

// Handle executes the queued email delivery.
func (j *SendQueuedMailJob) Handle(ctx context.Context) error {
	if j.Message == nil {
		return fmt.Errorf("mail: SendQueuedMailJob message is nil")
	}
	return Send(j.Message)
}
