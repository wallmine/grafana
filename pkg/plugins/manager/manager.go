package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/grafana/grafana/pkg/bus"
	"github.com/grafana/grafana/pkg/infra/fs"
	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/infra/metrics"
	"github.com/grafana/grafana/pkg/plugins"
	"github.com/grafana/grafana/pkg/plugins/backendplugin"
	"github.com/grafana/grafana/pkg/plugins/models"
	"github.com/grafana/grafana/pkg/registry"
	"github.com/grafana/grafana/pkg/services/sqlstore"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/grafana/grafana/pkg/util"
	"github.com/grafana/grafana/pkg/util/errutil"
)

type unsignedPluginConditionFunc = func(plugin *models.PluginBase) bool

type PluginScanner struct {
	pluginPath                    string
	errors                        []error
	backendPluginManager          backendplugin.Manager
	cfg                           *setting.Cfg
	requireSigned                 bool
	log                           log.Logger
	plugins                       map[string]*models.PluginBase
	allowUnsignedPluginsCondition unsignedPluginConditionFunc
}

func init() {
	registry.RegisterService(&PluginManager{})
}

// PluginManager manages Grafana plugins.
type PluginManager struct {
	BackendPluginManager backendplugin.Manager `inject:""`
	Cfg                  *setting.Cfg          `inject:""`
	SQLStore             *sqlstore.SQLStore    `inject:""`
	log                  log.Logger
	scanningErrors       []error

	// AllowUnsignedPluginsCondition changes the policy for allowing unsigned plugins. Signature validation only runs when plugins are starting
	// and running plugins will not be terminated if they violate the new policy.
	AllowUnsignedPluginsCondition unsignedPluginConditionFunc
	GrafanaLatestVersion          string
	GrafanaHasUpdate              bool
	DataSources                   map[string]*models.DataSourcePlugin
	Panels                        map[string]*models.PanelPlugin
	StaticRoutes                  []*models.PluginStaticRoute
	Apps                          map[string]*models.AppPlugin
	Plugins                       map[string]*models.PluginBase
	PluginTypes                   map[string]interface{}
	Renderer                      *models.RendererPlugin

	pluginScanningErrors map[string]PluginError
}

func (pm *PluginManager) Init() error {

	pm.log = log.New("plugins")

	pm.DataSources = map[string]*models.DataSourcePlugin{}
	pm.StaticRoutes = []*models.PluginStaticRoute{}
	pm.Panels = map[string]*models.PanelPlugin{}
	pm.Apps = map[string]*models.AppPlugin{}
	pm.Plugins = map[string]*models.PluginBase{}
	pm.PluginTypes = map[string]interface{}{
		"panel":      models.PanelPlugin{},
		"datasource": models.DataSourcePlugin{},
		"app":        models.AppPlugin{},
		"renderer":   models.RendererPlugin{},
	}
	pm.pluginScanningErrors = map[string]PluginError{}

	pm.log.Info("Starting plugin search")

	plugDir := filepath.Join(pm.Cfg.StaticRootPath, "app/plugins")
	pm.log.Debug("Scanning core plugin directory", "dir", plugDir)
	if err := pm.scan(plugDir, false); err != nil {
		return errutil.Wrapf(err, "failed to scan core plugin directory '%s'", plugDir)
	}

	plugDir = pm.Cfg.BundledPluginsPath
	pm.log.Debug("Scanning bundled plugins directory", "dir", plugDir)
	exists, err := fs.Exists(plugDir)
	if err != nil {
		return err
	}
	if exists {
		if err := pm.scan(plugDir, false); err != nil {
			return errutil.Wrapf(err, "failed to scan bundled plugins directory '%s'", plugDir)
		}
	}

	// check if plugins dir exists
	exists, err = fs.Exists(pm.Cfg.PluginsPath)
	if err != nil {
		return err
	}
	if !exists {
		if err = os.MkdirAll(pm.Cfg.PluginsPath, os.ModePerm); err != nil {
			pm.log.Error("failed to create external plugins directory", "dir", pm.Cfg.PluginsPath, "error", err)
		} else {
			pm.log.Info("External plugins directory created", "directory", pm.Cfg.PluginsPath)
		}
	} else {
		pm.log.Debug("Scanning external plugins directory", "dir", pm.Cfg.PluginsPath)
		if err := pm.scan(pm.Cfg.PluginsPath, true); err != nil {
			return errutil.Wrapf(err, "failed to scan external plugins directory '%s'",
				pm.Cfg.PluginsPath)
		}
	}

	if err := pm.scanPluginPaths(); err != nil {
		return err
	}

	for _, panel := range pm.Panels {
		panel.InitFrontendPlugin()
	}

	for _, ds := range pm.DataSources {
		ds.InitFrontendPlugin()
	}

	for _, app := range pm.Apps {
		app.InitApp()
	}

	if pm.Renderer != nil {
		pm.Renderer.InitFrontendPlugin()
	}

	for _, p := range pm.Plugins {
		if p.IsCorePlugin {
			p.Signature = plugins.PluginSignatureInternal
		} else {
			metrics.SetPluginBuildInformation(p.Id, p.Type, p.Info.Version)
		}
	}

	bus.AddHandler("plugins", pm.ImportDashboard)
	bus.AddEventListener(pm.handlePluginStateChanged)

	return nil
}

func (pm *PluginManager) Run(ctx context.Context) error {
	pm.updateAppDashboards()
	pm.checkForUpdates()

	ticker := time.NewTicker(time.Minute * 10)
	run := true

	for run {
		select {
		case <-ticker.C:
			pm.checkForUpdates()
		case <-ctx.Done():
			run = false
		}
	}

	return ctx.Err()
}

// GetTSDBPlugin gets a TSDBPlugin with a certain name. If none is found, nil is returned.
func (pm *PluginManager) GetTSDBPlugin(pluginID string) models.TSDBPlugin {
	return pm.DataSources[pluginID]
	// TODO: Look among other plugins
}

// scanPluginPaths scans configured plugin paths.
func (pm *PluginManager) scanPluginPaths() error {
	for pluginID, settings := range pm.Cfg.PluginSettings {
		path, exists := settings["path"]
		if !exists || path == "" {
			continue
		}

		if err := pm.scan(path, true); err != nil {
			return errutil.Wrapf(err, "failed to scan directory configured for plugin '%s': '%s'", pluginID, path)
		}
	}

	return nil
}

// scan a directory for plugins.
func (pm *PluginManager) scan(pluginDir string, requireSigned bool) error {
	scanner := &PluginScanner{
		pluginPath:                    pluginDir,
		backendPluginManager:          pm.BackendPluginManager,
		cfg:                           pm.Cfg,
		requireSigned:                 requireSigned,
		log:                           pm.log,
		plugins:                       map[string]*models.PluginBase{},
		allowUnsignedPluginsCondition: pm.AllowUnsignedPluginsCondition,
	}

	// 1st pass: Scan plugins, also mapping plugins to their respective directories
	if err := util.Walk(pluginDir, true, true, scanner.walker); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			pm.log.Debug("Couldn't scan directory since it doesn't exist", "pluginDir", pluginDir, "err", err)
			return nil
		}
		if errors.Is(err, os.ErrPermission) {
			pm.log.Debug("Couldn't scan directory due to lack of permissions", "pluginDir", pluginDir, "err", err)
			return nil
		}
		if pluginDir != "data/plugins" {
			pm.log.Warn("Could not scan dir", "pluginDir", pluginDir, "err", err)
		}
		return err
	}

	pm.log.Debug("Initial plugin loading done")

	// 2nd pass: Validate and register plugins
	for dpath, plugin := range scanner.plugins {
		// Try to find any root plugin
		ancestors := strings.Split(dpath, string(filepath.Separator))
		ancestors = ancestors[0 : len(ancestors)-1]
		aPath := ""
		if runtime.GOOS != "windows" && filepath.IsAbs(dpath) {
			aPath = "/"
		}
		for _, a := range ancestors {
			aPath = filepath.Join(aPath, a)
			if root, ok := scanner.plugins[aPath]; ok {
				plugin.Root = root
				break
			}
		}

		pm.log.Debug("Found plugin", "id", plugin.Id, "signature", plugin.Signature, "hasRoot", plugin.Root != nil)
		signingError := scanner.validateSignature(plugin)
		if signingError != nil {
			pm.log.Debug("Failed to validate plugin signature. Will skip loading", "id", plugin.Id,
				"signature", plugin.Signature, "status", signingError.ErrorCode)
			pm.pluginScanningErrors[plugin.Id] = *signingError
			continue
		}

		pm.log.Debug("Attempting to add plugin", "id", plugin.Id)

		pluginGoType, exists := pm.PluginTypes[plugin.Type]
		if !exists {
			return fmt.Errorf("unknown plugin type %q", plugin.Type)
		}

		jsonFPath := filepath.Join(plugin.PluginDir, "plugin.json")

		// External plugins need a module.js file for SystemJS to load
		if !strings.HasPrefix(jsonFPath, pm.Cfg.StaticRootPath) && !scanner.IsBackendOnlyPlugin(plugin.Type) {
			module := filepath.Join(plugin.PluginDir, "module.js")
			exists, err := fs.Exists(module)
			if err != nil {
				return err
			}
			if !exists {
				scanner.log.Warn("Plugin missing module.js",
					"name", plugin.Name,
					"warning", "Missing module.js, If you loaded this plugin from git, make sure to compile it.",
					"path", module)
			}
		}

		// nolint:gosec
		// We can ignore the gosec G304 warning on this one because `jsonFPath` is based
		// on plugin the folder structure on disk and not user input.
		reader, err := os.Open(jsonFPath)
		if err != nil {
			return err
		}
		defer func() {
			if err := reader.Close(); err != nil {
				scanner.log.Warn("Failed to close JSON file", "path", jsonFPath, "err", err)
			}
		}()

		jsonParser := json.NewDecoder(reader)

		loader := reflect.New(reflect.TypeOf(pluginGoType)).Interface().(models.PluginLoader)

		// Load the full plugin, and add it to manager
		if err := loader.Load(jsonParser, plugin, scanner.backendPluginManager); err != nil {
			if errors.Is(err, models.DuplicatePluginError{}) {
				pm.log.Warn("Plugin is duplicate", "error", err)
				scanner.errors = append(scanner.errors, err)
				continue
			}
			return err
		}
		pm.log.Debug("Successfully added plugin", "id", plugin.Id)
	}

	if len(scanner.errors) > 0 {
		pm.log.Warn("Some plugins failed to load", "errors", scanner.errors)
		pm.scanningErrors = scanner.errors
	}

	return nil
}

// GetDatasource returns a datasource based on passed pluginID if it exists
//
// This function fetches the datasource from the global variable DataSources in this package.
// Rather then refactor all dependencies on the global variable we can use this as an transition.
func (pm *PluginManager) GetDatasource(pluginID string) (*models.DataSourcePlugin, bool) {
	ds, exists := pm.DataSources[pluginID]
	return ds, exists
}

// ScanningErrors returns plugin scanning errors encountered.
func (pm *PluginManager) ScanningErrors() []PluginError {
	scanningErrs := make([]PluginError, 0)
	for id, e := range pm.pluginScanningErrors {
		scanningErrs = append(scanningErrs, PluginError{
			ErrorCode: e.ErrorCode,
			PluginID:  id,
		})
	}
	return scanningErrs
}

type EnabledPlugins struct {
	Panels      []*models.PanelPlugin
	DataSources map[string]*models.DataSourcePlugin
	Apps        []*models.AppPlugin
}

func (pm *PluginManager) GetEnabledPlugins(orgId int64) (*EnabledPlugins, error) {
	enabledPlugins := EnabledPlugins{
		Panels:      make([]*models.PanelPlugin, 0),
		DataSources: make(map[string]*models.DataSourcePlugin),
		Apps:        make([]*models.AppPlugin, 0),
	}
	pluginSettingMap, err := pm.GetPluginSettings(orgId)
	if err != nil {
		return nil, err
	}

	isPluginEnabled := func(pluginId string) bool {
		_, ok := pluginSettingMap[pluginId]
		return ok
	}

	for pluginId, app := range pm.Apps {
		if b, ok := pluginSettingMap[pluginId]; ok {
			app.Pinned = b.Pinned
			enabledPlugins.Apps = append(enabledPlugins.Apps, app)
		}
	}

	// add all plugins that are not part of an App.
	for dsId, ds := range pm.DataSources {
		if isPluginEnabled(ds.Id) {
			enabledPlugins.DataSources[dsId] = ds
		}
	}

	for _, panel := range pm.Panels {
		if isPluginEnabled(panel.Id) {
			enabledPlugins.Panels = append(enabledPlugins.Panels, panel)
		}
	}

	return &enabledPlugins, nil
}

func (s *PluginScanner) walker(currentPath string, f os.FileInfo, err error) error {
	// We scan all the subfolders for plugin.json (with some exceptions) so that we also load embedded plugins, for
	// example https://github.com/raintank/worldping-app/tree/master/dist/grafana-worldmap-panel worldmap panel plugin
	// is embedded in worldping app.
	if err != nil {
		return fmt.Errorf("filepath.Walk reported an error for %q: %w", currentPath, err)
	}

	if f.Name() == "node_modules" || f.Name() == "Chromium.app" {
		return util.ErrWalkSkipDir
	}

	if f.IsDir() {
		return nil
	}

	if f.Name() != "plugin.json" {
		return nil
	}

	if err := s.loadPlugin(currentPath); err != nil {
		s.log.Error("Failed to load plugin", "error", err, "pluginPath", filepath.Dir(currentPath))
		s.errors = append(s.errors, err)
	}

	return nil
}

func (s *PluginScanner) loadPlugin(pluginJSONFilePath string) error {
	s.log.Debug("Loading plugin", "path", pluginJSONFilePath)
	currentDir := filepath.Dir(pluginJSONFilePath)
	// nolint:gosec
	// We can ignore the gosec G304 warning on this one because `currentPath` is based
	// on plugin the folder structure on disk and not user input.
	reader, err := os.Open(pluginJSONFilePath)
	if err != nil {
		return err
	}
	defer func() {
		if err := reader.Close(); err != nil {
			s.log.Warn("Failed to close JSON file", "path", pluginJSONFilePath, "err", err)
		}
	}()

	jsonParser := json.NewDecoder(reader)
	pluginCommon := models.PluginBase{}
	if err := jsonParser.Decode(&pluginCommon); err != nil {
		return err
	}

	if pluginCommon.Id == "" || pluginCommon.Type == "" {
		return errors.New("did not find type or id properties in plugin.json")
	}

	pluginCommon.PluginDir = filepath.Dir(pluginJSONFilePath)
	pluginCommon.Files, err = collectPluginFilesWithin(pluginCommon.PluginDir)
	if err != nil {
		s.log.Warn("Could not collect plugin file information in directory", "pluginID", pluginCommon.Id, "dir", pluginCommon.PluginDir)
		return err
	}

	signatureState, err := getPluginSignatureState(s.log, &pluginCommon)
	if err != nil {
		s.log.Warn("Could not get plugin signature state", "pluginID", pluginCommon.Id, "err", err)
		return err
	}
	pluginCommon.Signature = signatureState.Status
	pluginCommon.SignatureType = signatureState.Type
	pluginCommon.SignatureOrg = signatureState.SigningOrg

	s.plugins[currentDir] = &pluginCommon

	return nil
}

func (*PluginScanner) IsBackendOnlyPlugin(pluginType string) bool {
	return pluginType == "renderer"
}

// validateSignature validates a plugin's signature.
func (s *PluginScanner) validateSignature(plugin *models.PluginBase) *PluginError {
	if plugin.Signature == plugins.PluginSignatureValid {
		s.log.Debug("Plugin has valid signature", "id", plugin.Id)
		return nil
	}

	if plugin.Root != nil {
		// If a descendant plugin with invalid signature, set signature to that of root
		if plugin.IsCorePlugin || plugin.Signature == plugins.PluginSignatureInternal {
			s.log.Debug("Not setting descendant plugin's signature to that of root since it's core or internal",
				"plugin", plugin.Id, "signature", plugin.Signature, "isCore", plugin.IsCorePlugin)
		} else {
			s.log.Debug("Setting descendant plugin's signature to that of root", "plugin", plugin.Id,
				"root", plugin.Root.Id, "signature", plugin.Signature, "rootSignature", plugin.Root.Signature)
			plugin.Signature = plugin.Root.Signature
			if plugin.Signature == plugins.PluginSignatureValid {
				s.log.Debug("Plugin has valid signature (inherited from root)", "id", plugin.Id)
				return nil
			}
		}
	} else {
		s.log.Debug("Non-valid plugin Signature", "pluginID", plugin.Id, "pluginDir", plugin.PluginDir,
			"state", plugin.Signature)
	}

	// For the time being, we choose to only require back-end plugins to be signed
	// NOTE: the state is calculated again when setting metadata on the object
	if !plugin.Backend || !s.requireSigned {
		return nil
	}

	switch plugin.Signature {
	case plugins.PluginSignatureUnsigned:
		if allowed := s.allowUnsigned(plugin); !allowed {
			s.log.Debug("Plugin is unsigned", "id", plugin.Id)
			s.errors = append(s.errors, fmt.Errorf("plugin %q is unsigned", plugin.Id))
			return &PluginError{
				ErrorCode: signatureMissing,
			}
		}
		s.log.Warn("Running an unsigned backend plugin", "pluginID", plugin.Id, "pluginDir",
			plugin.PluginDir)
		return nil
	case plugins.PluginSignatureInvalid:
		s.log.Debug("Plugin %q has an invalid signature", plugin.Id)
		s.errors = append(s.errors, fmt.Errorf("plugin %q has an invalid signature", plugin.Id))
		return &PluginError{
			ErrorCode: signatureInvalid,
		}
	case plugins.PluginSignatureModified:
		s.log.Debug("Plugin %q has a modified signature", plugin.Id)
		s.errors = append(s.errors, fmt.Errorf("plugin %q's signature has been modified", plugin.Id))
		return &PluginError{
			ErrorCode: signatureModified,
		}
	default:
		panic(fmt.Sprintf("Plugin %q has unrecognized plugin signature state %q", plugin.Id, plugin.Signature))
	}
}

func (s *PluginScanner) allowUnsigned(plugin *models.PluginBase) bool {
	if s.allowUnsignedPluginsCondition != nil {
		return s.allowUnsignedPluginsCondition(plugin)
	}

	if s.cfg.Env == setting.Dev {
		return true
	}

	for _, plug := range s.cfg.PluginsAllowUnsigned {
		if plug == plugin.Id {
			return true
		}
	}

	return false
}

func (pm *PluginManager) updateAppDashboards() {
	pm.log.Debug("Looking for App Dashboard Updates")

	rslt, err := pm.SQLStore.GetPluginSettings(0)
	if err != nil {
		pm.log.Error("Failed to get all plugin settings", "error", err)
		return
	}

	for _, pluginSetting := range rslt {
		// ignore disabled plugins
		if !pluginSetting.Enabled {
			continue
		}

		if pluginDef, exist := pm.Plugins[pluginSetting.PluginId]; exist {
			if pluginDef.Info.Version != pluginSetting.PluginVersion {
				pm.syncPluginDashboards(pluginDef, pluginSetting.OrgId)
			}
		}
	}
}

// gets plugin filenames that require verification for plugin signing
func collectPluginFilesWithin(rootDir string) ([]string, error) {
	var files []string

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name() != "MANIFEST.txt" {
			file, err := filepath.Rel(rootDir, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(file))
		}
		return nil
	})
	return files, err
}

func (pm *PluginManager) GetPluginMarkdown(pluginId string, name string) ([]byte, error) {
	plug, exists := pm.Plugins[pluginId]
	if !exists {
		return nil, models.PluginNotFoundError{pluginId}
	}

	// nolint:gosec
	// We can ignore the gosec G304 warning on this one because `plug.PluginDir` is based
	// on plugin the folder structure on disk and not user input.
	path := filepath.Join(plug.PluginDir, fmt.Sprintf("%s.md", strings.ToUpper(name)))
	exists, err := fs.Exists(path)
	if err != nil {
		return nil, err
	}
	if !exists {
		path = filepath.Join(plug.PluginDir, fmt.Sprintf("%s.md", strings.ToLower(name)))
	}

	exists, err = fs.Exists(path)
	if err != nil {
		return nil, err
	}
	if !exists {
		return make([]byte, 0), nil
	}

	// nolint:gosec
	// We can ignore the gosec G304 warning on this one because `plug.PluginDir` is based
	// on plugin the folder structure on disk and not user input.
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}
