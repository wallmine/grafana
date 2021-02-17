package rule

import (
	"errors"
	"time"

	"github.com/grafana/grafana/pkg/bus"
	"github.com/grafana/grafana/pkg/components/simplejson"
	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/services/alerting/alertingifaces"
	alertingerrors "github.com/grafana/grafana/pkg/services/alerting/errors"
	"github.com/grafana/grafana/pkg/tsdb/tsdbifaces"
)

var plog = log.New("alerting.rule")

// Rule is the in-memory version of an alert rule.
type Rule struct {
	ID                  int64
	OrgID               int64
	DashboardID         int64
	PanelID             int64
	Frequency           int64
	Name                string
	Message             string
	LastStateChange     time.Time
	For                 time.Duration
	NoDataState         models.NoDataOption
	ExecutionErrorState models.ExecutionErrorOption
	State               models.AlertStateType
	Conditions          []alertingifaces.Condition
	Notifications       []string
	AlertRuleTags       []*models.Tag

	StateChanges int64
}

// NewRuleFromDBAlert maps a db version of
// alert to an in-memory version.
func NewRuleFromDBAlert(ruleDef *models.Alert, logTranslationFailures bool,
	requestHandler tsdbifaces.RequestHandler) (*Rule, error) {
	model := &Rule{}
	model.ID = ruleDef.Id
	model.OrgID = ruleDef.OrgId
	model.DashboardID = ruleDef.DashboardId
	model.PanelID = ruleDef.PanelId
	model.Name = ruleDef.Name
	model.Message = ruleDef.Message
	model.State = ruleDef.State
	model.LastStateChange = ruleDef.NewStateDate
	model.For = ruleDef.For
	model.NoDataState = models.NoDataOption(ruleDef.Settings.Get("noDataState").MustString("no_data"))
	model.ExecutionErrorState = models.ExecutionErrorOption(ruleDef.Settings.Get("executionErrorState").MustString("alerting"))
	model.StateChanges = ruleDef.StateChanges

	model.Frequency = ruleDef.Frequency
	// frequency cannot be zero since that would not execute the alert rule.
	// so we fallback to 60 seconds if `Frequency` is missing
	if model.Frequency == 0 {
		model.Frequency = 60
	}

	for _, v := range ruleDef.Settings.Get("notifications").MustArray() {
		jsonModel := simplejson.NewFromAny(v)
		if id, err := jsonModel.Get("id").Int64(); err == nil {
			uid, err := translateNotificationIDToUID(id, ruleDef.OrgId)
			if err != nil {
				if !errors.Is(err, models.ErrAlertNotificationFailedTranslateUniqueID) {
					plog.Error("Failed to translate notification id to uid", "error", err.Error(),
						"dashboardId", model.DashboardID, "alert", model.Name, "panelId", model.PanelID, "notificationId", id)
				}

				if logTranslationFailures {
					plog.Warn("Unable to translate notification id to uid", "dashboardId", model.DashboardID,
						"alert", model.Name, "panelId", model.PanelID, "notificationId", id)
				}
			} else {
				model.Notifications = append(model.Notifications, uid)
			}
		} else if uid, err := jsonModel.Get("uid").String(); err == nil {
			model.Notifications = append(model.Notifications, uid)
		} else {
			return nil, alertingerrors.ValidationError{
				Reason:      "Neither id nor uid is specified in 'notifications' block, " + err.Error(),
				DashboardID: model.DashboardID, AlertID: model.ID, PanelID: model.PanelID,
			}
		}
	}
	model.AlertRuleTags = ruleDef.GetTagsFromSettings()

	for index, condition := range ruleDef.Settings.Get("conditions").MustArray() {
		conditionModel := simplejson.NewFromAny(condition)
		conditionType := conditionModel.Get("type").MustString()
		factory, exist := conditionFactories[conditionType]
		if !exist {
			return nil, alertingerrors.ValidationError{
				Reason:      "Unknown alert condition: " + conditionType,
				DashboardID: model.DashboardID, AlertID: model.ID, PanelID: model.PanelID,
			}
		}
		queryCondition, err := factory(conditionModel, index, requestHandler)
		if err != nil {
			return nil, alertingerrors.ValidationError{
				Err: err, DashboardID: model.DashboardID, AlertID: model.ID, PanelID: model.PanelID,
			}
		}
		model.Conditions = append(model.Conditions, queryCondition)
	}

	if len(model.Conditions) == 0 {
		return nil, alertingerrors.ValidationError{Reason: "Alert is missing conditions"}
	}

	return model, nil
}

func translateNotificationIDToUID(id int64, orgID int64) (string, error) {
	notificationUID, err := getAlertNotificationUIDByIDAndOrgID(id, orgID)
	if err != nil {
		return "", err
	}

	return notificationUID, nil
}

func getAlertNotificationUIDByIDAndOrgID(notificationID int64, orgID int64) (string, error) {
	query := &models.GetAlertNotificationUidQuery{
		OrgId: orgID,
		Id:    notificationID,
	}

	if err := bus.Dispatch(query); err != nil {
		return "", err
	}

	return query.Result, nil
}

// ConditionFactory is the function signature for creating `Conditions`.
type ConditionFactory func(model *simplejson.Json, index int, requestHandler tsdbifaces.RequestHandler) (
	alertingifaces.Condition, error)

var conditionFactories = make(map[string]ConditionFactory)

// RegisterCondition adds support for alerting conditions.
func RegisterCondition(typeName string, factory ConditionFactory) {
	conditionFactories[typeName] = factory
}
