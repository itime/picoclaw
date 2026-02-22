package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// Manifest 插件清单
type Manifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Keywords    []string `json:"keywords"`
	Bin         string   `json:"bin"`
	HelpCmd     string   `json:"help_cmd"`
	Platforms   []string `json:"platforms,omitempty"`
}

// Plugin 表示一个已加载的插件
type Plugin struct {
	Manifest   Manifest
	Path       string // 插件目录路径
	BinaryPath string // 可执行文件路径
	HelpText   string // 缓存的帮助文本
}

// PluginManager 管理所有插件
type PluginManager struct {
	pluginsDir string
	plugins    map[string]*Plugin
	state      *StateStore
}

// NewPluginManager 创建插件管理器
func NewPluginManager(pluginsDir string) *PluginManager {
	if pluginsDir == "" {
		home, _ := os.UserHomeDir()
		pluginsDir = filepath.Join(home, ".picoclaw", "plugins")
	}
	return &PluginManager{
		pluginsDir: pluginsDir,
		plugins:    make(map[string]*Plugin),
		state:      NewStateStore(pluginsDir),
	}
}

// LoadAll 加载所有插件
func (pm *PluginManager) LoadAll() error {
	entries, err := os.ReadDir(pm.pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 插件目录不存在，正常情况
		}
		return fmt.Errorf("读取插件目录失败: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pluginPath := filepath.Join(pm.pluginsDir, entry.Name())
		plugin, err := pm.loadPlugin(pluginPath)
		if err != nil {
			logger.WarnCF("plugins", "加载插件失败",
				map[string]interface{}{
					"plugin": entry.Name(),
					"error":  err.Error(),
				})
			continue
		}

		// 检查插件是否启用
		if !pm.state.IsEnabled(plugin.Manifest.Name) {
			logger.InfoCF("plugins", "插件已禁用，跳过",
				map[string]interface{}{
					"name": plugin.Manifest.Name,
				})
			continue
		}

		pm.plugins[plugin.Manifest.Name] = plugin
		logger.InfoCF("plugins", "插件已加载",
			map[string]interface{}{
				"name":    plugin.Manifest.Name,
				"version": plugin.Manifest.Version,
			})
	}

	return nil
}

// loadPlugin 加载单个插件
func (pm *PluginManager) loadPlugin(pluginPath string) (*Plugin, error) {
	manifestPath := filepath.Join(pluginPath, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("读取 manifest.json 失败: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("解析 manifest.json 失败: %w", err)
	}

	binaryPath := filepath.Join(pluginPath, manifest.Bin)
	if _, err := os.Stat(binaryPath); err != nil {
		return nil, fmt.Errorf("可执行文件不存在: %s", manifest.Bin)
	}

	// 获取帮助文本
	helpCmd := manifest.HelpCmd
	if helpCmd == "" {
		helpCmd = "--help"
	}

	helpText := ""
	cmd := exec.Command(binaryPath, helpCmd)
	output, err := cmd.Output()
	if err == nil {
		helpText = string(output)
	}

	return &Plugin{
		Manifest:   manifest,
		Path:       pluginPath,
		BinaryPath: binaryPath,
		HelpText:   helpText,
	}, nil
}

// Get 获取插件
func (pm *PluginManager) Get(name string) (*Plugin, bool) {
	plugin, ok := pm.plugins[name]
	return plugin, ok
}

// List 列出所有插件
func (pm *PluginManager) List() []*Plugin {
	plugins := make([]*Plugin, 0, len(pm.plugins))
	for _, p := range pm.plugins {
		plugins = append(plugins, p)
	}
	return plugins
}

// MatchKeyword 根据关键词匹配插件
func (pm *PluginManager) MatchKeyword(keyword string) []*Plugin {
	keyword = strings.ToLower(keyword)
	var matched []*Plugin

	for _, plugin := range pm.plugins {
		for _, kw := range plugin.Manifest.Keywords {
			if strings.Contains(strings.ToLower(kw), keyword) ||
				strings.Contains(keyword, strings.ToLower(kw)) {
				matched = append(matched, plugin)
				break
			}
		}
	}

	return matched
}

// CreateTools 为所有插件创建 Tool
func (pm *PluginManager) CreateTools() []tools.Tool {
	var toolList []tools.Tool
	for _, plugin := range pm.plugins {
		toolList = append(toolList, NewPluginTool(plugin))
	}
	return toolList
}

// PluginTool 将插件包装为 Tool
type PluginTool struct {
	plugin *Plugin
}

// NewPluginTool 创建插件工具
func NewPluginTool(plugin *Plugin) *PluginTool {
	return &PluginTool{plugin: plugin}
}

func (t *PluginTool) Name() string {
	return t.plugin.Manifest.Name
}

func (t *PluginTool) Description() string {
	desc := t.plugin.Manifest.Description
	if t.plugin.HelpText != "" {
		// 添加帮助文本摘要
		lines := strings.Split(t.plugin.HelpText, "\n")
		if len(lines) > 10 {
			lines = lines[:10]
		}
		desc += "\n\n帮助信息:\n" + strings.Join(lines, "\n")
	}
	return desc
}

func (t *PluginTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": "要执行的子命令和参数，例如 'project list' 或 'workitem create --title \"任务\"'",
			},
			"json_output": map[string]interface{}{
				"type":        "boolean",
				"description": "是否使用 JSON 格式输出（默认 true）",
				"default":     true,
			},
		},
		"required": []string{"command"},
	}
}

func (t *PluginTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return tools.ErrorResult("缺少 command 参数")
	}

	// 解析命令参数
	cmdArgs := parseCommand(command)

	// 检查是否需要 JSON 输出
	jsonOutput := true
	if v, ok := args["json_output"].(bool); ok {
		jsonOutput = v
	}

	if jsonOutput {
		// 检查是否已有 --json 参数
		hasJSON := false
		for _, arg := range cmdArgs {
			if arg == "--json" {
				hasJSON = true
				break
			}
		}
		if !hasJSON {
			cmdArgs = append(cmdArgs, "--json")
		}
	}

	// 执行插件命令
	cmd := exec.CommandContext(ctx, t.plugin.BinaryPath, cmdArgs...)
	output, err := cmd.CombinedOutput()

	if err != nil {
		// 检查是否有输出（可能是业务错误而非执行错误）
		if len(output) > 0 {
			return tools.ErrorResult(string(output))
		}
		return tools.ErrorResult(fmt.Sprintf("执行失败: %v", err))
	}

	return tools.NewToolResult(string(output))
}

// parseCommand 解析命令字符串为参数数组
func parseCommand(command string) []string {
	var args []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)

	for _, r := range command {
		switch {
		case r == '"' || r == '\'':
			if inQuote && r == quoteChar {
				inQuote = false
				quoteChar = 0
			} else if !inQuote {
				inQuote = true
				quoteChar = r
			} else {
				current.WriteRune(r)
			}
		case r == ' ' && !inQuote:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 {
		args = append(args, current.String())
	}

	return args
}

// GetPluginsSummary 获取所有插件的摘要信息（用于 system prompt）
func (pm *PluginManager) GetPluginsSummary() string {
	if len(pm.plugins) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## 可用插件\n\n")

	for _, plugin := range pm.plugins {
		sb.WriteString(fmt.Sprintf("### %s\n", plugin.Manifest.Name))
		sb.WriteString(fmt.Sprintf("%s\n", plugin.Manifest.Description))
		sb.WriteString(fmt.Sprintf("关键词: %s\n\n", strings.Join(plugin.Manifest.Keywords, ", ")))

		if plugin.HelpText != "" {
			sb.WriteString("```\n")
			sb.WriteString(plugin.HelpText)
			sb.WriteString("\n```\n\n")
		}
	}

	return sb.String()
}

// Enable 启用插件
func (pm *PluginManager) Enable(name string) error {
	return pm.state.SetEnabled(name, true)
}

// Disable 禁用插件
func (pm *PluginManager) Disable(name string) error {
	return pm.state.SetEnabled(name, false)
}

// IsEnabled 检查插件是否启用
func (pm *PluginManager) IsEnabled(name string) bool {
	return pm.state.IsEnabled(name)
}

// GetState 获取状态存储（用于 CLI）
func (pm *PluginManager) GetState() *StateStore {
	return pm.state
}

// ListAllManifests 列出所有已安装的插件（包括禁用的）
func (pm *PluginManager) ListAllManifests() ([]ManifestWithState, error) {
	entries, err := os.ReadDir(pm.pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []ManifestWithState
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		manifestPath := filepath.Join(pm.pluginsDir, entry.Name(), "manifest.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}

		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			continue
		}

		state := pm.state.GetState(manifest.Name)
		result = append(result, ManifestWithState{
			Manifest: manifest,
			Enabled:  state.Enabled,
			Source:   state.Source,
		})
	}

	return result, nil
}

// ManifestWithState 带状态的插件清单
type ManifestWithState struct {
	Manifest Manifest
	Enabled  bool
	Source   string
}
