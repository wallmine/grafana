package manager

import (
	"github.com/grafana/grafana/pkg/models"
)

func (pm *PluginManager) GetPluginSettings(orgID int64) (map[string]*models.PluginSettingInfoDTO, error) {
	rslt, err := pm.SQLStore.GetPluginSettings(orgID)
	if err != nil {
		return nil, err
	}

	pluginMap := make(map[string]*models.PluginSettingInfoDTO)
	for _, plug := range rslt {
		pluginMap[plug.PluginId] = plug
	}

	for _, pluginDef := range Plugins {
		// ignore entries that exists
		if _, ok := pluginMap[pluginDef.Id]; ok {
			continue
		}

		// default to enabled true
		opt := &models.PluginSettingInfoDTO{
			PluginId: pluginDef.Id,
			OrgId:    orgId,
			Enabled:  true,
		}

		// apps are disabled by default
		if pluginDef.Type == PluginTypeApp {
			opt.Enabled = false
		}

		// if it's included in app check app settings
		if pluginDef.IncludedInAppId != "" {
			// app components are by default disabled
			opt.Enabled = false

			if appSettings, ok := pluginMap[pluginDef.IncludedInAppId]; ok {
				opt.Enabled = appSettings.Enabled
			}
		}

		pluginMap[pluginDef.Id] = opt
	}

	return pluginMap, nil
}

// IsAppInstalled checks if an app plugin with provided plugin ID is installed.
func IsAppInstalled(pluginID string) bool {
	_, exists := Apps[pluginID]
	return exists
}
