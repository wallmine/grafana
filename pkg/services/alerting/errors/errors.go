package errors

import "fmt"

// ValidationError is a typed error with metadata
// about the validation error.
type ValidationError struct {
	Reason      string
	Err         error
	AlertID     int64
	DashboardID int64
	PanelID     int64
}

func (e ValidationError) Error() string {
	extraInfo := e.Reason
	if e.AlertID != 0 {
		extraInfo = fmt.Sprintf("%s AlertId: %v", extraInfo, e.AlertID)
	}

	if e.PanelID != 0 {
		extraInfo = fmt.Sprintf("%s PanelId: %v", extraInfo, e.PanelID)
	}

	if e.DashboardID != 0 {
		extraInfo = fmt.Sprintf("%s DashboardId: %v", extraInfo, e.DashboardID)
	}

	if e.Err != nil {
		return fmt.Sprintf("alert validation error: %s%s", e.Err.Error(), extraInfo)
	}

	return fmt.Sprintf("alert validation error: %s", extraInfo)
}
