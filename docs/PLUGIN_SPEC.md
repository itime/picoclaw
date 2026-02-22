# picoclaw 插件开发规范

版本: 1.0.0

## 概述

picoclaw 插件是独立的 CLI 工具，通过标准输入输出与 picoclaw 交互。本规范定义了插件的结构、接口和行为标准，确保 LLM 能正确理解和调用插件。

## 与 MCP 的关系

picoclaw 插件机制与 MCP (Model Context Protocol) 是**互补**而非冲突的：

| 特性 | picoclaw 插件 | MCP |
|------|--------------|-----|
| 通信方式 | CLI 调用 | JSON-RPC 2.0 |
| 状态 | 无状态 | 可有状态 |
| 发现 | manifest.json + --help | initialize RPC |
| 适用场景 | 简单工具、脚本 | 复杂服务、需要状态 |

**兼容策略**：
- CLI 插件继续使用当前机制
- MCP server 可通过 MCPTool 包装为 tools.Tool
- Agent loop 统一处理两种类型

**何时选择 CLI 插件**：
- 工具逻辑简单，无需保持状态
- 希望独立开发、独立部署
- 需要跨语言实现

**何时选择 MCP**：
- 需要长连接、状态保持
- 需要资源订阅、变更通知
- 已有 MCP server 实现

## 目录结构

```
~/.picoclaw/plugins/{plugin-name}/
├── manifest.json    # 必须 - 插件元信息
├── {binary}         # 必须 - 可执行文件
└── README.md        # 可选 - 详细文档
```

## manifest.json

```json
{
  "name": "example",
  "version": "1.0.0",
  "description": "一句话描述插件功能",
  "keywords": ["关键词1", "关键词2"],
  "bin": "example",
  "help_cmd": "--help",
  "platforms": ["darwin-arm64", "darwin-amd64", "linux-amd64"]
}
```

### 字段说明

| 字段 | 必须 | 说明 |
|------|------|------|
| name | 是 | 插件名，小写字母、数字、连字符 |
| version | 是 | 语义化版本号 |
| description | 是 | 一句话描述，LLM 用于判断相关性 |
| keywords | 是 | 触发词数组，用户提到时加载插件帮助 |
| bin | 是 | 可执行文件名 |
| help_cmd | 否 | 帮助参数，默认 `--help` |
| platforms | 否 | 支持的平台 |

## CLI 接口规范

### 基本要求

1. **必须支持 `--help`** - 输出命令帮助
2. **必须支持 `--json`** - 输出 JSON 格式
3. **stdout** - 正常输出
4. **stderr** - 错误信息
5. **退出码** - 0 成功，非 0 失败

### --help 输出格式

```
{工具名} - {一句话描述}

用法:
  {工具名} <命令> [参数]

命令:
  {命令}    {描述}

全局参数:
  --json    输出 JSON 格式
  --help    显示帮助

使用 "{工具名} {命令} --help" 查看命令详情
```

### 子命令 --help 格式

```
{命令描述}

用法:
  {工具名} {命令} [参数]

参数:
  --{name}    {描述} (必填)
  --{name}    {描述} (默认: {value})

示例:
  {工具名} {命令} --{arg}=value
```

### --json 输出格式

成功:

```json
{
  "success": true,
  "data": {
    "key": "value"
  }
}
```

失败:

```json
{
  "success": false,
  "error": "错误描述"
}
```

列表数据:

```json
{
  "success": true,
  "data": {
    "items": [...],
    "total": 100
  }
}
```

## 完整示例

### manifest.json

```json
{
  "name": "yunxiao",
  "version": "1.0.0",
  "description": "阿里云云效 DevOps 工具，管理项目、工作项、流水线",
  "keywords": ["云效", "yunxiao", "devops", "流水线", "pipeline", "阿里云"],
  "bin": "yunxiao",
  "help_cmd": "--help"
}
```

### --help 输出

```
yunxiao - 阿里云云效 DevOps CLI

用法:
  yunxiao <命令> [参数]

命令:
  project list              列出所有项目
  project get <id>          获取项目详情
  
  issue list <project>      列出工作项
  issue get <id>            获取工作项详情
  issue create <project>    创建工作项
  issue update <id>         更新工作项
  
  pipeline list <project>   列出流水线
  pipeline run <id>         运行流水线
  pipeline status <id>      查看运行状态

全局参数:
  --json      输出 JSON 格式
  --help      显示帮助
  --config    配置文件路径 (默认: ~/.yunxiao/config.yaml)

使用 "yunxiao <命令> --help" 查看命令详情
```

### 子命令 --help

```
创建工作项

用法:
  yunxiao issue create <project-id> [参数]

参数:
  --title       工作项标题 (必填)
  --type        类型: task/bug/story (默认: task)
  --desc        描述
  --assignee    指派人 ID
  --priority    优先级: P0/P1/P2/P3 (默认: P2)
  --sprint      迭代 ID

示例:
  yunxiao issue create proj-123 --title "修复登录bug" --type bug --assignee user-456
  yunxiao issue create proj-123 --title "新功能" --desc "详细描述" --json
```

### 命令输出示例

```bash
$ yunxiao project list --json
```

```json
{
  "success": true,
  "data": {
    "items": [
      {
        "id": "proj-123",
        "name": "后端服务",
        "description": "主要后端项目"
      },
      {
        "id": "proj-456", 
        "name": "前端应用",
        "description": "Web 前端"
      }
    ],
    "total": 2
  }
}
```

```bash
$ yunxiao issue create proj-123 --title "测试" --json
```

```json
{
  "success": true,
  "data": {
    "id": "issue-789",
    "title": "测试",
    "status": "open",
    "created_at": "2026-02-21T12:30:00Z"
  }
}
```

错误情况:

```bash
$ yunxiao issue create proj-123 --json
```

```json
{
  "success": false,
  "error": "缺少必填参数: --title"
}
```

## 插件安装

### 从 GitHub 安装

```bash
picoclaw plugin install github:owner/repo
```

插件仓库需包含:
- `manifest.json`
- `releases/` 或 GitHub Releases 中的二进制文件

### 从 URL 安装

```bash
picoclaw plugin install https://example.com/plugin.tar.gz
```

压缩包结构:

```
plugin.tar.gz
└── {plugin-name}/
    ├── manifest.json
    └── {binary}
```

### 本地安装

```bash
picoclaw plugin install ./my-plugin
```

## 插件管理命令

```bash
# 列出已安装插件
picoclaw plugin list

# 安装
picoclaw plugin install <source>

# 卸载
picoclaw plugin remove <name>

# 启用/禁用
picoclaw plugin enable <name>
picoclaw plugin disable <name>

# 更新
picoclaw plugin update <name>
picoclaw plugin update --all
```

## 最佳实践

### 帮助信息

1. 描述简洁明了，LLM 能快速理解
2. 参数标注必填/可选和默认值
3. 提供常用示例
4. 子命令帮助要详细

### JSON 输出

1. 始终包含 `success` 字段
2. 数据放在 `data` 字段
3. 列表用 `items` + `total`
4. 错误信息要有意义

### 错误处理

1. 参数校验失败返回清晰错误
2. 网络错误要区分
3. 权限问题要提示

### 配置

1. 支持配置文件和环境变量
2. 敏感信息（token）不要硬编码
3. 首次使用引导配置

## 配置文件

插件配置建议放在 `~/.{plugin-name}/config.yaml`:

```yaml
# ~/.yunxiao/config.yaml
endpoint: https://devops.aliyuncs.com
token: ${YUNXIAO_TOKEN}
default_project: proj-123
```

支持环境变量:

```bash
export YUNXIAO_TOKEN=xxx
```

## 版本兼容

- 遵循语义化版本
- 破坏性变更升级主版本号
- manifest.json 中声明最低 picoclaw 版本（可选）

```json
{
  "picoclaw": ">=1.0.0"
}
```

## MCP 集成（规划中）

picoclaw 计划支持 MCP server 作为工具来源。配置示例：

```json
{
  "mcp": {
    "servers": [
      {
        "name": "filesystem",
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/dir"],
        "enabled": true
      },
      {
        "name": "github",
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-github"],
        "env": {
          "GITHUB_TOKEN": "${GITHUB_TOKEN}"
        },
        "enabled": true
      }
    ]
  }
}
```

MCP server 的工具将自动注册到 agent，与 CLI 插件统一管理。
