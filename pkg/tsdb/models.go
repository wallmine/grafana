package tsdb

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/grafana/grafana-plugin-sdk-go/data"
	"github.com/grafana/grafana/pkg/components/null"
	"github.com/grafana/grafana/pkg/components/simplejson"
	"github.com/grafana/grafana/pkg/models"
)

// TsdbQuery contains all information about a query request.
type TsdbQuery struct {
	TimeRange *TimeRange
	Queries   []*Query
	Headers   map[string]string
	Debug     bool
	User      *models.SignedInUser
}

type Query struct {
	RefId         string             `json:"refId"`
	Model         *simplejson.Json   `json:"model,omitempty"`
	DataSource    *models.DataSource `json:"datasource"`
	MaxDataPoints int64              `json:"maxDataPoints"`
	IntervalMs    int64              `json:"intervalMs"`
	QueryType     string             `json:"queryType"`
}

type Response struct {
	Results map[string]*QueryResult `json:"results"`
	Message string                  `json:"message,omitempty"`
}

type QueryResult struct {
	Error       error            `json:"-"`
	ErrorString string           `json:"error,omitempty"`
	RefId       string           `json:"refId"`
	Meta        *simplejson.Json `json:"meta,omitempty"`
	Series      TimeSeriesSlice  `json:"series"`
	Tables      []*Table         `json:"tables"`
	Dataframes  DataFrames       `json:"dataframes"`
}

// UnmarshalJSON deserializes a QueryResult from JSON.
//
// Deserialization support is required by tests.
func (r *QueryResult) UnmarshalJSON(b []byte) error {
	m := map[string]interface{}{}
	// TODO: Use JSON decoder
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}

	refID, ok := m["refId"].(string)
	if !ok {
		return fmt.Errorf("can't decode field refId - not a string")
	}
	var meta *simplejson.Json
	if m["meta"] != nil {
		mm, ok := m["meta"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("can't decode field meta - not a JSON object")
		}
		meta = simplejson.NewFromAny(mm)
	}
	var series TimeSeriesSlice
	/* TODO
	if m["series"] != nil {
	}
	*/
	var tables []*Table
	if m["tables"] != nil {
		ts, ok := m["tables"].([]interface{})
		if !ok {
			return fmt.Errorf("can't decode field tables - not an array of Tables")
		}
		for _, ti := range ts {
			tm, ok := ti.(map[string]interface{})
			if !ok {
				return fmt.Errorf("can't decode field tables - not an array of Tables")
			}
			var columns []TableColumn
			cs, ok := tm["columns"].([]interface{})
			if !ok {
				return fmt.Errorf("can't decode field tables - not an array of Tables")
			}
			for _, ci := range cs {
				cm, ok := ci.(map[string]interface{})
				if !ok {
					return fmt.Errorf("can't decode field tables - not an array of Tables")
				}
				val, ok := cm["text"].(string)
				if !ok {
					return fmt.Errorf("can't decode field tables - not an array of Tables")
				}

				columns = append(columns, TableColumn{Text: val})
			}

			rs, ok := tm["rows"].([]interface{})
			if !ok {
				return fmt.Errorf("can't decode field tables - not an array of Tables")
			}
			var rows []RowValues
			for _, ri := range rs {
				vals, ok := ri.([]interface{})
				if !ok {
					return fmt.Errorf("can't decode field tables - not an array of Tables")
				}
				rows = append(rows, vals)
			}

			tables = append(tables, &Table{
				Columns: columns,
				Rows:    rows,
			})
		}
	}

	var dfs *dataFrames
	if m["dataframes"] != nil {
		raw, ok := m["dataframes"].([]interface{})
		if !ok {
			return fmt.Errorf("can't decode field dataframes - not an array of byte arrays")
		}

		var encoded [][]byte
		for _, ra := range raw {
			encS, ok := ra.(string)
			if !ok {
				return fmt.Errorf("can't decode field dataframes - not an array of byte arrays")
			}
			enc, err := base64.StdEncoding.DecodeString(encS)
			if err != nil {
				return fmt.Errorf("can't decode field dataframes - not an array of arrow frames")
			}
			encoded = append(encoded, enc)
		}
		decoded, err := data.UnmarshalArrowFrames(encoded)
		if err != nil {
			return err
		}
		dfs = &dataFrames{
			decoded: decoded,
			encoded: encoded,
		}
	}

	r.RefId = refID
	r.Meta = meta
	r.Series = series
	r.Tables = tables
	if dfs != nil {
		r.Dataframes = dfs
	}
	return nil
}

type TimeSeries struct {
	Name   string            `json:"name"`
	Points TimeSeriesPoints  `json:"points"`
	Tags   map[string]string `json:"tags,omitempty"`
}

type Table struct {
	Columns []TableColumn `json:"columns"`
	Rows    []RowValues   `json:"rows"`
}

type TableColumn struct {
	Text string `json:"text"`
}

type RowValues []interface{}
type TimePoint [2]null.Float
type TimeSeriesPoints []TimePoint
type TimeSeriesSlice []*TimeSeries

func NewQueryResult() *QueryResult {
	return &QueryResult{
		Series: make(TimeSeriesSlice, 0),
	}
}

func NewTimePoint(value null.Float, timestamp float64) TimePoint {
	return TimePoint{value, null.FloatFrom(timestamp)}
}
