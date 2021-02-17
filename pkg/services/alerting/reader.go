package alerting

import (
	"sync"

	"github.com/grafana/grafana/pkg/bus"
	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/infra/metrics"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/services/alerting/rule"
	"github.com/grafana/grafana/pkg/tsdb/tsdbifaces"
)

type ruleReader interface {
	fetch() []*rule.Rule
}

type defaultRuleReader struct {
	sync.RWMutex
	log        log.Logger
	reqHandler tsdbifaces.RequestHandler
}

func newRuleReader(reqHandler tsdbifaces.RequestHandler) *defaultRuleReader {
	ruleReader := &defaultRuleReader{
		log:        log.New("alerting.ruleReader"),
		reqHandler: reqHandler,
	}

	return ruleReader
}

func (arr *defaultRuleReader) fetch() []*rule.Rule {
	cmd := &models.GetAllAlertsQuery{}

	if err := bus.Dispatch(cmd); err != nil {
		arr.log.Error("Could not load alerts", "error", err)
		return []*rule.Rule{}
	}

	res := make([]*rule.Rule, 0)
	for _, ruleDef := range cmd.Result {
		if model, err := rule.NewRuleFromDBAlert(ruleDef, false, arr.reqHandler); err != nil {
			arr.log.Error("Could not build alert model for rule", "ruleId", ruleDef.Id, "error", err)
		} else {
			res = append(res, model)
		}
	}

	metrics.MAlertingActiveAlerts.Set(float64(len(res)))
	return res
}
