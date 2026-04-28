// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/s-zx/crest/pkg/aiusechat/uctypes"
	"github.com/s-zx/crest/pkg/wavebase"
	"github.com/s-zx/crest/pkg/wconfig"
)

const ConnectTimeout = 30 * time.Second
const ListToolsTimeout = 15 * time.Second
const CallToolTimeout = 120 * time.Second

var (
	managerInstance *MCPManager
	managerOnce     sync.Once
)

func GetManager() *MCPManager {
	managerOnce.Do(func() {
		managerInstance = &MCPManager{
			servers: make(map[string]*mcpConn),
		}
		managerInstance.loadFromConfig()
		managerInstance.watchConfig()
	})
	return managerInstance
}

type MCPManager struct {
	lock    sync.Mutex
	servers map[string]*mcpConn
}

type mcpConn struct {
	name       string
	config     wconfig.MCPServerConfig
	client     mcpclient.MCPClient
	tools      []mcplib.Tool
	connected  bool
	lastError  string
	lock       sync.Mutex
}

func (m *MCPManager) loadFromConfig() {
	fullConfig := wconfig.GetWatcher().GetFullConfig()
	servers := fullConfig.Settings.AiMcpServers
	if len(servers) == 0 {
		return
	}
	m.reconcile(servers)
}

func (m *MCPManager) watchConfig() {
	wconfig.GetWatcher().RegisterUpdateHandler(func(config wconfig.FullConfigType) {
		m.reconcile(config.Settings.AiMcpServers)
	})
}

func (m *MCPManager) reconcile(desired map[string]wconfig.MCPServerConfig) {
	m.lock.Lock()
	defer m.lock.Unlock()

	seen := make(map[string]bool)

	for name, cfg := range desired {
		if !cfg.IsEnabled() {
			continue
		}
		seen[name] = true
		existing, exists := m.servers[name]
		if exists && configEqual(existing.config, cfg) && existing.connected {
			continue
		}
		if exists {
			go existing.disconnect()
		}
		conn := &mcpConn{name: name, config: cfg}
		m.servers[name] = conn
		go conn.connect()
	}

	for name, conn := range m.servers {
		if !seen[name] {
			go conn.disconnect()
			delete(m.servers, name)
		}
	}
}

func (m *MCPManager) GetAllTools() []uctypes.ToolDefinition {
	m.lock.Lock()
	servers := make([]*mcpConn, 0, len(m.servers))
	for _, conn := range m.servers {
		servers = append(servers, conn)
	}
	m.lock.Unlock()

	var tools []uctypes.ToolDefinition
	for _, conn := range servers {
		conn.lock.Lock()
		if !conn.connected || len(conn.tools) == 0 {
			conn.lock.Unlock()
			continue
		}
		connClient := conn.client
		connTools := make([]mcplib.Tool, len(conn.tools))
		copy(connTools, conn.tools)
		serverName := conn.name
		conn.lock.Unlock()

		for _, tool := range connTools {
			callFn := makeCallFn(connClient, serverName)
			tools = append(tools, MCPToolToDefinition(serverName, tool, callFn))
		}
	}
	return tools
}

func (m *MCPManager) Shutdown() {
	m.lock.Lock()
	servers := make([]*mcpConn, 0, len(m.servers))
	for _, conn := range m.servers {
		servers = append(servers, conn)
	}
	m.servers = make(map[string]*mcpConn)
	m.lock.Unlock()

	for _, conn := range servers {
		conn.disconnect()
	}
}

func (m *MCPManager) GetServerStatus() map[string]string {
	m.lock.Lock()
	defer m.lock.Unlock()
	status := make(map[string]string, len(m.servers))
	for name, conn := range m.servers {
		conn.lock.Lock()
		if conn.connected {
			status[name] = fmt.Sprintf("connected (%d tools)", len(conn.tools))
		} else if conn.lastError != "" {
			status[name] = fmt.Sprintf("error: %s", conn.lastError)
		} else {
			status[name] = "connecting"
		}
		conn.lock.Unlock()
	}
	return status
}

func (c *mcpConn) connect() {
	c.lock.Lock()
	defer c.lock.Unlock()

	cfg := c.config
	transportType := cfg.Type
	if transportType == "" && cfg.Command != "" {
		transportType = "stdio"
	}

	var mcpCl mcpclient.MCPClient
	var err error

	ctx, cancel := context.WithTimeout(context.Background(), ConnectTimeout)
	defer cancel()

	switch transportType {
	case "stdio":
		if cfg.Command == "" {
			c.lastError = "stdio transport requires 'command'"
			log.Printf("mcp[%s]: %s\n", c.name, c.lastError)
			return
		}
		env := envMapToSlice(cfg.Env)
		mcpCl, err = mcpclient.NewStdioMCPClient(cfg.Command, env, cfg.Args...)
		if err != nil {
			c.lastError = fmt.Sprintf("failed to create stdio client: %v", err)
			log.Printf("mcp[%s]: %s\n", c.name, c.lastError)
			return
		}

	case "sse":
		if cfg.URL == "" {
			c.lastError = "SSE transport requires 'url'"
			log.Printf("mcp[%s]: %s\n", c.name, c.lastError)
			return
		}
		mcpCl, err = mcpclient.NewSSEMCPClient(cfg.URL)
		if err != nil {
			c.lastError = fmt.Sprintf("failed to create SSE client: %v", err)
			log.Printf("mcp[%s]: %s\n", c.name, c.lastError)
			return
		}

	case "http":
		if cfg.URL == "" {
			c.lastError = "HTTP transport requires 'url'"
			log.Printf("mcp[%s]: %s\n", c.name, c.lastError)
			return
		}
		mcpCl, err = mcpclient.NewStreamableHttpClient(cfg.URL)
		if err != nil {
			c.lastError = fmt.Sprintf("failed to create HTTP client: %v", err)
			log.Printf("mcp[%s]: %s\n", c.name, c.lastError)
			return
		}

	default:
		c.lastError = fmt.Sprintf("unsupported transport type: %q", transportType)
		log.Printf("mcp[%s]: %s\n", c.name, c.lastError)
		return
	}

	initReq := mcplib.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcplib.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcplib.Implementation{
		Name:    "crest",
		Version: wavebase.WaveVersion,
	}

	_, err = mcpCl.Initialize(ctx, initReq)
	if err != nil {
		c.lastError = fmt.Sprintf("initialize failed: %v", err)
		log.Printf("mcp[%s]: %s\n", c.name, c.lastError)
		mcpCl.Close()
		return
	}

	toolsCtx, toolsCancel := context.WithTimeout(context.Background(), ListToolsTimeout)
	defer toolsCancel()

	result, err := mcpCl.ListTools(toolsCtx, mcplib.ListToolsRequest{})
	if err != nil {
		c.lastError = fmt.Sprintf("list tools failed: %v", err)
		log.Printf("mcp[%s]: %s\n", c.name, c.lastError)
		mcpCl.Close()
		return
	}

	c.client = mcpCl
	c.tools = result.Tools
	c.connected = true
	c.lastError = ""
	log.Printf("mcp[%s]: connected, %d tools available\n", c.name, len(result.Tools))
}

func (c *mcpConn) disconnect() {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.client != nil {
		if err := c.client.Close(); err != nil {
			log.Printf("mcp[%s]: close error: %v\n", c.name, err)
		}
	}
	c.client = nil
	c.tools = nil
	c.connected = false
}

func makeCallFn(cl mcpclient.MCPClient, serverName string) ToolCallFn {
	return func(ctx context.Context, toolName string, args map[string]any) (*mcplib.CallToolResult, error) {
		callCtx, cancel := context.WithTimeout(ctx, CallToolTimeout)
		defer cancel()
		req := mcplib.CallToolRequest{}
		req.Params.Name = toolName
		req.Params.Arguments = args
		result, err := cl.CallTool(callCtx, req)
		if err != nil {
			return nil, fmt.Errorf("mcp[%s] call %s failed: %w", serverName, toolName, err)
		}
		return result, nil
	}
}

func envMapToSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	result := make([]string, 0, len(env))
	for k, v := range env {
		result = append(result, k+"="+v)
	}
	return result
}

func configEqual(a, b wconfig.MCPServerConfig) bool {
	if a.Command != b.Command || a.Type != b.Type || a.URL != b.URL {
		return false
	}
	if len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Args {
		if a.Args[i] != b.Args[i] {
			return false
		}
	}
	if len(a.Env) != len(b.Env) {
		return false
	}
	for k, v := range a.Env {
		if b.Env[k] != v {
			return false
		}
	}
	return true
}
