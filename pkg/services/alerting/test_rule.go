package alerting

import (
	"context"
	"fmt"

	"github.com/grafana/grafana/pkg/bus"
	"github.com/grafana/grafana/pkg/components/simplejson"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/services/alerting/dashboards"
	"github.com/grafana/grafana/pkg/services/alerting/evalcontext"
	"github.com/grafana/grafana/pkg/services/alerting/rule"
)

// AlertTestCommand initiates an test evaluation
// of an alert rule.
type AlertTestCommand struct {
	Dashboard *simplejson.Json
	PanelID   int64
	OrgID     int64
	User      *models.SignedInUser

	Result *evalcontext.EvalContext
}

func init() {
	bus.AddHandler("alerting", handleAlertTestCommand)
}

func handleAlertTestCommand(cmd *AlertTestCommand) error {
	dash := models.NewDashboardFromJson(cmd.Dashboard)

	extractor := dashboards.NewDashAlertExtractor(dash, cmd.OrgID, cmd.User, nil)
	alerts, err := extractor.GetAlerts()
	if err != nil {
		return err
	}

	for _, alert := range alerts {
		if alert.PanelId == cmd.PanelID {
			rule, err := rule.NewRuleFromDBAlert(alert, true, nil)
			if err != nil {
				return err
			}

			cmd.Result = testAlertRule(rule)
			return nil
		}
	}

	return fmt.Errorf("could not find alert with panel ID %d", cmd.PanelID)
}

func testAlertRule(rule *rule.Rule) *evalcontext.EvalContext {
	handler := NewEvalHandler()

	context := evalcontext.NewEvalContext(context.Background(), rule, fakeRequestValidator{})
	context.IsTestRun = true
	context.IsDebug = true

	handler.Eval(context)
	context.Rule.State = context.GetNewState()

	return context
}
