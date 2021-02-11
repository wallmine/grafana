package tsdb

import (
	"context"
	"fmt"

	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/plugins/manager"
	pluginmodels "github.com/grafana/grafana/pkg/plugins/models"
	"github.com/grafana/grafana/pkg/registry"
	"github.com/grafana/grafana/pkg/setting"
)

func init() {
	registry.Register(&registry.Descriptor{
		Name:     "TSDBService",
		Instance: &Service{},
	})
}

type HandleRequestFunc func(ctx context.Context, dsInfo *models.DataSource, req pluginmodels.TSDBQuery) (pluginmodels.TSDBResponse, error)

type TSDBQueryEndpoint interface {
	Query(ctx context.Context, ds *models.DataSource, query pluginmodels.TSDBQuery) (pluginmodels.TSDBResponse, error)
}

type GetTSDBQueryEndpointFn func(dsInfo *models.DataSource) (TSDBQueryEndpoint, error)

// Service handles requests to TSDB data sources.
type Service struct {
	Cfg           *setting.Cfg          `inject:""`
	PluginManager manager.PluginManager `inject:""`

	registry map[string]GetTSDBQueryEndpointFn
}

// Init initialises the service.
func (s *Service) Init() error {
	return nil
}

func (s *Service) RegisterTSDBQueryEndpoint(pluginID string, fn GetTSDBQueryEndpointFn) {
	s.registry[pluginID] = fn
}

func (s *Service) HandleRequest(ctx context.Context, dsInfo *models.DataSource, req pluginmodels.TSDBQuery) (
	pluginmodels.TSDBResponse, error) {
	plugin := s.PluginManager.GetTSDBPlugin(dsInfo.Type)
	if plugin == nil {
		return pluginmodels.TSDBResponse{}, fmt.Errorf("could not find plugin corresponding to data source type: %q",
			dsInfo.Type)
	}

	return plugin.TSDBQuery(ctx, dsInfo, req)
}
