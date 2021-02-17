package alertingifaces

import (
	"github.com/grafana/grafana/pkg/services/alerting/alertingmodels"
)

// ConditionResult is the result of a condition evaluation.
type ConditionResult struct {
	Firing      bool
	NoDataFound bool
	Operator    string
	EvalMatches []*alertingmodels.EvalMatch
}

type EvalContext interface {
}

// Condition is responsible for evaluating an alert condition.
type Condition interface {
	Eval(result EvalContext) (*ConditionResult, error)
}
