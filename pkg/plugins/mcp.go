// Package plugins provides plugin management for picoclaw.
//
// MCP (Model Context Protocol) Compatibility
//
// This file defines interfaces and types for future MCP integration.
// MCP is Anthropic's protocol for LLM-tool communication, using JSON-RPC 2.0.
//
// Current picoclaw plugin architecture:
//   - CLI-based: plugins are standalone executables
//   - Stateless: each invocation is independent
//   - Discovery: via manifest.json + --help
//
// MCP architecture:
//   - JSON-RPC 2.0 over stdio or HTTP/SSE
//   - Stateful: long-running server connections
//   - Discovery: via initialize/tools/list RPC
//
// Compatibility strategy:
//   1. CLI plugins remain as-is (PluginTool)
//   2. MCP servers can be wrapped as MCPTool
//   3. Both implement the same tools.Tool interface
//   4. Agent loop treats them uniformly
//
// Future implementation:
//   - MCPClient: manages connection to MCP server
//   - MCPTool: wraps MCP server as tools.Tool
//   - MCPManager: discovers and manages MCP servers

package plugins

// MCPToolDefinition represents an MCP tool definition.
// This mirrors the MCP protocol's tool schema.
type MCPToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// MCPResource represents an MCP resource.
type MCPResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// MCPPrompt represents an MCP prompt template.
type MCPPrompt struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Arguments   []MCPPromptArgument    `json:"arguments,omitempty"`
}

// MCPPromptArgument represents an argument for an MCP prompt.
type MCPPromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// MCPServerConfig represents configuration for an MCP server.
type MCPServerConfig struct {
	// Name is the server identifier
	Name string `json:"name"`

	// Command is the executable to run (for stdio transport)
	Command string `json:"command,omitempty"`

	// Args are command-line arguments
	Args []string `json:"args,omitempty"`

	// Env are environment variables
	Env map[string]string `json:"env,omitempty"`

	// URL is the server URL (for HTTP/SSE transport)
	URL string `json:"url,omitempty"`

	// Transport is "stdio" or "http" (default: stdio)
	Transport string `json:"transport,omitempty"`

	// Enabled indicates if this server should be loaded
	Enabled bool `json:"enabled"`
}

// MCPCapabilities represents server capabilities from initialize response.
type MCPCapabilities struct {
	Tools     *MCPToolsCapability     `json:"tools,omitempty"`
	Resources *MCPResourcesCapability `json:"resources,omitempty"`
	Prompts   *MCPPromptsCapability   `json:"prompts,omitempty"`
}

// MCPToolsCapability indicates tool support.
type MCPToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// MCPResourcesCapability indicates resource support.
type MCPResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// MCPPromptsCapability indicates prompt support.
type MCPPromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// Note: Full MCP client implementation will be added when needed.
// The types above provide the foundation for MCP integration.
//
// To implement MCP support:
// 1. Create MCPClient that handles JSON-RPC communication
// 2. Create MCPTool that wraps MCP tools as tools.Tool
// 3. Create MCPManager that manages multiple MCP servers
// 4. Add MCP server configuration to config.json
// 5. Load MCP servers alongside CLI plugins in agent loop
