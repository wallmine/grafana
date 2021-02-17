package alertingmodels

import (
	"github.com/grafana/grafana/pkg/components/null"
	"github.com/grafana/grafana/pkg/infra/log"
)

var plog = log.New("alertingmodels")

// ResultLogEntry represents log data for the alert evaluation.
type ResultLogEntry struct {
	Message string
	Data    interface{}
}

// EvalMatch represents the series violating the threshold.
type EvalMatch struct {
	Value  null.Float        `json:"value"`
	Metric string            `json:"metric"`
	Tags   map[string]string `json:"tags"`
}
