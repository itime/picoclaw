package plugins

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Installer 插件安装器
type Installer struct {
	pluginsDir string
	state      *StateStore
}

// NewInstaller 创建安装器
func NewInstaller(pluginsDir string) *Installer {
	if pluginsDir == "" {
		home, _ := os.UserHomeDir()
		pluginsDir = filepath.Join(home, ".picoclaw", "plugins")
	}
	return &Installer{
		pluginsDir: pluginsDir,
		state:      NewStateStore(pluginsDir),
	}
}

// InstallFromGitHub 从 GitHub 安装插件
// repo 格式: owner/repo 或 owner/repo/path
func (i *Installer) InstallFromGitHub(ctx context.Context, repo string) error {
	parts := strings.Split(repo, "/")
	if len(parts) < 2 {
		return fmt.Errorf("无效的仓库格式，应为 owner/repo 或 owner/repo/path")
	}

	owner := parts[0]
	repoName := parts[1]
	subPath := ""
	if len(parts) > 2 {
		subPath = strings.Join(parts[2:], "/")
	}

	// 获取最新 release
	releaseURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repoName)
	req, err := http.NewRequestWithContext(ctx, "GET", releaseURL, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("获取 release 信息失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		// 尝试直接从 main 分支下载
		return i.installFromBranch(ctx, owner, repoName, subPath, "main", "github:"+repo)
	}

	var release struct {
		Assets []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("解析 release 信息失败: %w", err)
	}

	// 查找匹配当前平台的资源
	platform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
	var downloadURL string

	for _, asset := range release.Assets {
		if strings.Contains(asset.Name, platform) {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	if downloadURL == "" {
		// 没有预编译版本，从源码安装
		return i.installFromBranch(ctx, owner, repoName, subPath, "main", "github:"+repo)
	}

	return i.downloadAndInstall(ctx, downloadURL, subPath, "github:"+repo)
}

// installFromBranch 从分支下载源码
func (i *Installer) installFromBranch(ctx context.Context, owner, repo, subPath, branch, source string) error {
	// 下载 manifest.json
	manifestURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s/manifest.json",
		owner, repo, branch, subPath)
	if subPath == "" {
		manifestURL = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/manifest.json",
			owner, repo, branch)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", manifestURL, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("下载 manifest.json 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("manifest.json 不存在 (HTTP %d)", resp.StatusCode)
	}

	var manifest Manifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return fmt.Errorf("解析 manifest.json 失败: %w", err)
	}

	// 创建插件目录
	pluginDir := filepath.Join(i.pluginsDir, manifest.Name)
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return fmt.Errorf("创建插件目录失败: %w", err)
	}

	// 保存 manifest.json
	manifestPath := filepath.Join(pluginDir, "manifest.json")
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(manifestPath, manifestData, 0644); err != nil {
		return fmt.Errorf("保存 manifest.json 失败: %w", err)
	}

	// 提示用户需要手动编译
	return fmt.Errorf("插件 %s 没有预编译版本，请手动编译后复制到 %s", manifest.Name, pluginDir)
}

// downloadAndInstall 下载并安装压缩包
func (i *Installer) downloadAndInstall(ctx context.Context, url, subPath, source string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("下载失败 (HTTP %d)", resp.StatusCode)
	}

	// 创建临时文件
	tmpFile, err := os.CreateTemp("", "picoclaw-plugin-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return fmt.Errorf("保存下载文件失败: %w", err)
	}

	// 解压
	tmpFile.Seek(0, 0)
	pluginName, err := i.extractTarGz(tmpFile, i.pluginsDir)
	if err != nil {
		return err
	}

	// 记录安装来源
	if pluginName != "" && source != "" {
		i.state.SetSource(pluginName, source)
	}

	return nil
}

// extractTarGz 解压 tar.gz 文件，返回插件名称
func (i *Installer) extractTarGz(r io.Reader, dest string) (string, error) {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return "", err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var pluginName string

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		target := filepath.Join(dest, header.Name)

		// 提取插件名称（第一级目录）
		if pluginName == "" {
			parts := strings.Split(header.Name, "/")
			if len(parts) > 0 && parts[0] != "" {
				pluginName = parts[0]
			}
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return "", err
			}

			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return "", err
			}

			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return "", err
			}
			f.Close()
		}
	}

	return pluginName, nil
}

// InstallFromURL 从 URL 安装插件
func (i *Installer) InstallFromURL(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	return i.downloadAndInstall(ctx, url, "", "url:"+url)
}

// InstallFromLocal 从本地目录安装插件
func (i *Installer) InstallFromLocal(src string) error {
	// 读取 manifest
	manifestPath := filepath.Join(src, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("读取 manifest.json 失败: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("解析 manifest.json 失败: %w", err)
	}

	// 创建目标目录
	dest := filepath.Join(i.pluginsDir, manifest.Name)
	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("创建插件目录失败: %w", err)
	}

	// 复制文件
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		destPath := filepath.Join(dest, entry.Name())

		if entry.IsDir() {
			continue // 跳过子目录
		}

		data, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("读取 %s 失败: %w", entry.Name(), err)
		}

		// 保持可执行权限
		info, _ := entry.Info()
		mode := info.Mode()

		if err := os.WriteFile(destPath, data, mode); err != nil {
			return fmt.Errorf("写入 %s 失败: %w", entry.Name(), err)
		}
	}

	// 记录安装来源
	absPath, _ := filepath.Abs(src)
	i.state.SetSource(manifest.Name, "local:"+absPath)

	return nil
}

// Uninstall 卸载插件
func (i *Installer) Uninstall(name string) error {
	pluginDir := filepath.Join(i.pluginsDir, name)

	if _, err := os.Stat(pluginDir); os.IsNotExist(err) {
		return fmt.Errorf("插件 %s 不存在", name)
	}

	// 移除状态
	i.state.Remove(name)

	return os.RemoveAll(pluginDir)
}

// ListInstalled 列出已安装的插件
func (i *Installer) ListInstalled() ([]Manifest, error) {
	entries, err := os.ReadDir(i.pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var manifests []Manifest
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		manifestPath := filepath.Join(i.pluginsDir, entry.Name(), "manifest.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}

		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			continue
		}

		manifests = append(manifests, manifest)
	}

	return manifests, nil
}

// ListInstalledWithState 列出已安装的插件（带状态）
func (i *Installer) ListInstalledWithState() ([]ManifestWithState, error) {
	entries, err := os.ReadDir(i.pluginsDir)
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

		manifestPath := filepath.Join(i.pluginsDir, entry.Name(), "manifest.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}

		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			continue
		}

		state := i.state.GetState(manifest.Name)
		result = append(result, ManifestWithState{
			Manifest: manifest,
			Enabled:  state.Enabled,
			Source:   state.Source,
		})
	}

	return result, nil
}

// Enable 启用插件
func (i *Installer) Enable(name string) error {
	return i.state.SetEnabled(name, true)
}

// Disable 禁用插件
func (i *Installer) Disable(name string) error {
	return i.state.SetEnabled(name, false)
}

// Update 更新插件（从原始来源重新安装）
func (i *Installer) Update(ctx context.Context, name string) error {
	state := i.state.GetState(name)
	if state.Source == "" {
		return fmt.Errorf("插件 %s 没有记录安装来源，无法自动更新", name)
	}

	source := state.Source

	switch {
	case strings.HasPrefix(source, "github:"):
		repo := strings.TrimPrefix(source, "github:")
		return i.InstallFromGitHub(ctx, repo)

	case strings.HasPrefix(source, "url:"):
		url := strings.TrimPrefix(source, "url:")
		return i.InstallFromURL(ctx, url)

	case strings.HasPrefix(source, "local:"):
		path := strings.TrimPrefix(source, "local:")
		return i.InstallFromLocal(path)

	default:
		return fmt.Errorf("未知的安装来源格式: %s", source)
	}
}

// UpdateAll 更新所有插件
func (i *Installer) UpdateAll(ctx context.Context) (updated []string, failed map[string]error) {
	failed = make(map[string]error)

	manifests, err := i.ListInstalled()
	if err != nil {
		failed["_list"] = err
		return
	}

	for _, m := range manifests {
		if err := i.Update(ctx, m.Name); err != nil {
			failed[m.Name] = err
		} else {
			updated = append(updated, m.Name)
		}
	}

	return
}
