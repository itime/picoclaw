# PicoClaw 开发指南

## 项目概述

PicoClaw 是一个轻量级个人 AI Agent，支持多渠道（Discord、Telegram、WhatsApp 等）接入，具备 tool calling 能力。

## 目录结构

```
/Users/lzw/code/my/go/picoclaw/
├── cmd/picoclaw/          # 主程序入口
├── pkg/
│   ├── agent/             # Agent 核心逻辑
│   ├── channels/          # 渠道适配器（Discord、Telegram 等）
│   ├── config/            # 配置管理
│   ├── providers/         # LLM Provider（Claude、OpenAI 等）
│   └── tools/             # Tool 实现
├── build/                 # 编译输出
├── Makefile               # 构建脚本
└── deploy-stable.sh       # 部署脚本
```

## 部署架构

### Stable 版本（生产环境）

- **二进制**: `/usr/local/bin/picoclaw-stable`
- **配置**: `/Users/lzw/.picoclaw/config-stable.json`
- **端口**: 18791
- **日志**: `/Users/lzw/log/supervisor/picoclaw/out.log`
- **Supervisor 配置**: `/opt/homebrew/etc/supervisor.d/picoclaw.conf`

### 开发版本

- **源码**: `/Users/lzw/code/my/go/picoclaw/`
- **配置**: `/Users/lzw/.picoclaw/config.json`
- **端口**: 18790（默认）

## 常用命令

### 构建与部署

```bash
# 开发构建（快速，无版本信息）
cd /Users/lzw/code/my/go/picoclaw
go build -o picoclaw ./cmd/picoclaw

# 正式构建（带版本信息）
make build

# 部署到 stable（一键）
./deploy-stable.sh
```

### Supervisor 管理

```bash
# 查看状态
/usr/local/bin/supervisord ctl status picoclaw

# 重启服务
sudo /usr/local/bin/supervisord ctl restart picoclaw

# 查看日志
tail -f /Users/lzw/log/supervisor/picoclaw/out.log

# 重新加载配置
/usr/local/bin/supervisord ctl reload
```

### 版本查看

```bash
/usr/local/bin/picoclaw-stable version
# 输出示例:
# 🦞 picoclaw 87aee78-dirty (git: 87aee789)
#   Build: 2026-02-21T04:18:21+0800
#   Go: go1.25.7
```

## 配置说明

### 环境变量

- `PICOCLAW_CONFIG`: 指定配置文件路径（默认 `~/.picoclaw/config.json`）

### 配置文件差异

| 配置项 | config.json (开发) | config-stable.json (生产) |
|--------|-------------------|--------------------------|
| gateway.port | 18790 | 18791 |

## 开发工作流

1. **修改代码** → 在 `/Users/lzw/code/my/go/picoclaw/` 开发
2. **本地测试** → `go build && ./picoclaw gateway`
3. **部署 stable** → `./deploy-stable.sh`
4. **验证** → 检查 Discord Bot 响应

## 关键文件

### Tool 开发

- `pkg/tools/opencode_tool.go` - opencode 集成 tool
- `pkg/tools/result.go` - ToolResult 结构定义

### Provider 开发

- `pkg/providers/claude_provider.go` - Claude API 适配
- `pkg/providers/types.go` - 通用类型定义

### 渠道开发

- `pkg/channels/discord.go` - Discord 单 Bot 模式
- `pkg/channels/discord_multi.go` - Discord 多 Agent 模式（待启用）

## 注意事项

### Claude Provider Tool Call 格式

Claude API 的 tool_use block 需要正确处理 Arguments：
- 优先使用 `tc.Arguments`（map 类型）
- 回退到 `tc.Function.Arguments`（string 类型，需 JSON 解析）
- 同时处理 `tc.Name` 和 `tc.Function.Name`

### opencode Tool

- 使用 `opencode run [message] --dir [workDir]` 命令
- 二进制路径: `/Users/lzw/.bun/bin/opencode`
- 默认超时: 10 分钟

## API 配置

当前使用本地代理：
- Base URL: `http://localhost:8990`
- API Key: `sk-kiro-rs-qazWSXedcRFV123456`
- Model: `claude-opus-4-6`
