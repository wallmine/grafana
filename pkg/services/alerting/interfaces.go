package alerting

import (
	"context"
	"time"

	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/services/alerting/evalcontext"
	"github.com/grafana/grafana/pkg/services/alerting/job"
	"github.com/grafana/grafana/pkg/services/alerting/rule"
)

type evalHandler interface {
	Eval(evalContext *evalcontext.EvalContext)
}

type scheduler interface {
	Tick(time time.Time, execQueue chan *job.Job)
	Update(rules []*rule.Rule)
}

// Notifier is responsible for sending alert notifications.
type Notifier interface {
	Notify(evalContext *evalcontext.EvalContext) error
	GetType() string
	NeedsImage() bool

	// ShouldNotify checks this evaluation should send an alert notification
	ShouldNotify(ctx context.Context, evalContext *evalcontext.EvalContext, notificationState *models.AlertNotificationState) bool

	GetNotifierUID() string
	GetIsDefault() bool
	GetSendReminder() bool
	GetDisableResolveMessage() bool
	GetFrequency() time.Duration
}

type notifierState struct {
	notifier Notifier
	state    *models.AlertNotificationState
}

type notifierStateSlice []*notifierState

func (notifiers notifierStateSlice) ShouldUploadImage() bool {
	for _, ns := range notifiers {
		if ns.notifier.NeedsImage() {
			return true
		}
	}

	return false
}
