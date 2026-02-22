package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// PluginState 插件状态
type PluginState struct {
	Enabled bool   `json:"enabled"`
	Source  string `json:"source,omitempty"` // 安装来源，用于更新
}

// StateStore 插件状态存储
type StateStore struct {
	Plugins map[string]*PluginState `json:"plugins"`
	mu      sync.RWMutex
	path    string
}

// NewStateStore 创建状态存储
func NewStateStore(pluginsDir string) *StateStore {
	path := filepath.Join(pluginsDir, "state.json")
	store := &StateStore{
		Plugins: make(map[string]*PluginState),
		path:    path,
	}
	store.load()
	return store
}

// load 从文件加载状态
func (s *StateStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	return json.Unmarshal(data, s)
}

// save 保存状态到文件
func (s *StateStore) save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}

	return os.WriteFile(s.path, data, 0644)
}

// GetState 获取插件状态
func (s *StateStore) GetState(name string) *PluginState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	state, ok := s.Plugins[name]
	if !ok {
		// 默认启用
		return &PluginState{Enabled: true}
	}
	return state
}

// SetEnabled 设置插件启用状态
func (s *StateStore) SetEnabled(name string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Plugins[name] == nil {
		s.Plugins[name] = &PluginState{}
	}
	s.Plugins[name].Enabled = enabled

	return s.save()
}

// SetSource 设置插件安装来源
func (s *StateStore) SetSource(name, source string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Plugins[name] == nil {
		s.Plugins[name] = &PluginState{Enabled: true}
	}
	s.Plugins[name].Source = source

	return s.save()
}

// IsEnabled 检查插件是否启用
func (s *StateStore) IsEnabled(name string) bool {
	return s.GetState(name).Enabled
}

// Remove 移除插件状态
func (s *StateStore) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.Plugins, name)
	return s.save()
}

// List 列出所有插件状态
func (s *StateStore) List() map[string]*PluginState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]*PluginState)
	for k, v := range s.Plugins {
		result[k] = v
	}
	return result
}
