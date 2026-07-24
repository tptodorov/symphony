package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/tptodorov/symphony/go/internal/agent"
	"github.com/tptodorov/symphony/go/internal/domain"
	"github.com/tptodorov/symphony/go/internal/tools"
)

const defaultReadTimeout = 5 * time.Second
const dynamicToolPayloadMaxBytes = 16 * 1024
const dynamicToolTruncatedMarker = "\n...[truncated]"
const continuationPrompt = "Continue working on the same issue. Re-check the tracker state and move the issue toward the workflow-defined handoff state. Do not repeat context already present in this thread."

type Runner struct {
	Command     string
	ReadTimeout time.Duration
	TurnTimeout time.Duration
}

func New(command string) *Runner { return &Runner{Command: command} }

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type turnResult struct {
	Completed bool
	Err       error
}

type appServerClient struct {
	ctx         context.Context
	stdin       io.WriteCloser
	req         agent.RunRequest
	events      chan<- agent.Event
	readTimeout time.Duration
	logs        *agent.RunLogs

	mu        sync.Mutex
	nextID    int
	pending   map[string]chan rpcMessage
	threadID  string
	turnID    string
	sessionID string
	started   bool
	usage     domain.TokenUsage
	turnDone  chan turnResult
}

func (r *Runner) Run(ctx context.Context, req agent.RunRequest, events chan<- agent.Event) agent.Result {
	command := req.Command
	if command == "" {
		command = r.Command
	}
	if command == "" {
		command = "codex app-server"
	}
	if req.Workspace == "" {
		return agent.Result{SessionID: req.SessionID, Err: fmt.Errorf("workspace path is required")}
	}
	turnTimeout := req.TurnTimeout
	if turnTimeout == 0 {
		turnTimeout = r.TurnTimeout
	}
	if turnTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, turnTimeout)
		defer cancel()
	}
	readTimeout := req.ReadTimeout
	if readTimeout == 0 {
		readTimeout = r.ReadTimeout
	}
	if readTimeout == 0 {
		readTimeout = defaultReadTimeout
	}
	runLogs, err := agent.OpenRunLogs(req.Logs)
	if err != nil {
		return agent.Result{SessionID: req.SessionID, Logs: req.Logs, Err: fmt.Errorf("open agent logs: %w", err)}
	}
	defer runLogs.Close()
	finish := func(err error) agent.Result {
		res := agent.Result{SessionID: req.SessionID, Logs: req.Logs, Err: err}
		runLogs.WriteResult(res)
		return res
	}

	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Dir = req.Workspace
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return finish(fmt.Errorf("open stdin: %w", err))
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return finish(fmt.Errorf("open stdout: %w", err))
	}
	var stderr bytes.Buffer
	cmd.Stderr = runLogs.StderrWriter(&stderr)
	if err := cmd.Start(); err != nil {
		return finish(fmt.Errorf("start codex: %w", err))
	}

	client := &appServerClient{
		ctx:         ctx,
		stdin:       stdin,
		req:         req,
		events:      events,
		readTimeout: readTimeout,
		logs:        runLogs,
		nextID:      1,
		pending:     map[string]chan rpcMessage{},
		sessionID:   req.SessionID,
		turnDone:    make(chan turnResult, 1),
	}
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		client.scan(stdout)
	}()
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	defer stopAppServer(cmd, stdin, waitDone, scanDone)

	if err := client.initialize(); err != nil {
		return client.result(err, false)
	}
	threadID, err := client.startThread()
	if err != nil {
		return client.result(err, false)
	}
	maxTurns := req.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 1
	}
	var res turnResult
	for turnIndex := 0; turnIndex < maxTurns; turnIndex++ {
		prompt := req.Prompt
		if turnIndex > 0 {
			prompt = continuationPrompt
		}
		client.prepareTurn(threadID)
		turnID, err := client.startTurn(threadID, prompt)
		if err != nil {
			return client.result(err, false)
		}
		client.setTurn(threadID, turnID)

		res = client.waitForTurn()
		if res.Err != nil || !res.Completed {
			return client.result(res.Err, res.Completed)
		}
	}
	return client.result(res.Err, res.Completed)
}

func stopAppServer(cmd *exec.Cmd, stdin io.Closer, waitDone <-chan error, scanDone <-chan struct{}) {
	_ = stdin.Close()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		<-waitDone
	}
	<-scanDone
}

func (c *appServerClient) initialize() error {
	_, err := c.request("initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "symphony_go",
			"title":   "Symphony Go",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{"experimentalApi": true},
	})
	if err != nil {
		return err
	}
	return c.notify("initialized", map[string]any{})
}

func (c *appServerClient) startThread() (string, error) {
	result, err := c.request("thread/start", c.threadStartParams())
	if err != nil {
		return "", err
	}
	id := nestedID(result, "thread")
	if id == "" {
		return "", fmt.Errorf("response_error: thread/start did not return thread.id")
	}
	return id, nil
}

func (c *appServerClient) startTurn(threadID, prompt string) (string, error) {
	result, err := c.request("turn/start", c.turnStartParams(threadID, prompt))
	if err != nil {
		return "", err
	}
	id := nestedID(result, "turn")
	if id == "" {
		return "", fmt.Errorf("response_error: turn/start did not return turn.id")
	}
	return id, nil
}

func (c *appServerClient) threadStartParams() map[string]any {
	params := map[string]any{"cwd": c.req.Workspace, "serviceName": "symphony_go"}
	policy := policyMap(c.req.Policy)
	if v, ok := policy["approval_policy"]; ok {
		params["approvalPolicy"] = v
	}
	if v, ok := policy["thread_sandbox"]; ok {
		params["sandbox"] = v
	}
	if tools := c.dynamicTools(); len(tools) > 0 {
		params["dynamicTools"] = tools
	}
	return params
}

func (c *appServerClient) turnStartParams(threadID, prompt string) map[string]any {
	params := map[string]any{
		"threadId": threadID,
		"cwd":      c.req.Workspace,
		"input":    []map[string]any{{"type": "text", "text": prompt}},
	}
	policy := policyMap(c.req.Policy)
	if v, ok := policy["approval_policy"]; ok {
		params["approvalPolicy"] = v
	}
	if v, ok := policy["turn_sandbox_policy"]; ok {
		params["sandboxPolicy"] = v
	}
	return params
}

func (c *appServerClient) dynamicTools() []map[string]any {
	out := []map[string]any{}
	if c.req.EnableBeadsCLI {
		out = append(out, map[string]any{
			"name":        "beads_cli",
			"description": "Execute bd CLI commands using the configured tracker.bd_command in the repository working directory.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"args": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []string{"args"},
			},
			"deferLoading": false,
		})
	}
	if c.req.EnableLinearGraphQL {
		out = append(out, map[string]any{
			"name":        "linear_graphql",
			"description": "Execute one Linear GraphQL query or mutation using Symphony's configured Linear endpoint and auth.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":     map[string]any{"type": "string"},
					"variables": map[string]any{"type": "object", "additionalProperties": true},
				},
				"required": []string{"query"},
			},
			"deferLoading": false,
		})
	}
	if c.req.EnableJiraREST {
		out = append(out, map[string]any{
			"name":        "jira_rest",
			"description": "Execute one Jira REST API request using Symphony's configured Jira endpoint and auth.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"method": map[string]any{"type": "string"},
					"path":   map[string]any{"type": "string"},
					"query":  map[string]any{"type": "object", "additionalProperties": true},
					"body":   map[string]any{},
				},
				"required": []string{"method", "path"},
			},
			"deferLoading": false,
		})
	}
	return out
}

func (c *appServerClient) request(method string, params any) (json.RawMessage, error) {
	id := c.nextRequestID()
	key := strconv.Itoa(id)
	ch := make(chan rpcMessage, 1)
	c.mu.Lock()
	c.pending[key] = ch
	c.mu.Unlock()
	if err := c.send(map[string]any{"method": method, "id": id, "params": params}); err != nil {
		c.deletePending(key)
		return nil, err
	}
	timer := time.NewTimer(c.readTimeout)
	defer timer.Stop()
	select {
	case msg := <-ch:
		if msg.Error != nil {
			return nil, fmt.Errorf("response_error: %s", msg.Error.Message)
		}
		return msg.Result, nil
	case <-timer.C:
		c.deletePending(key)
		return nil, fmt.Errorf("response_timeout: %s timed out after %s", method, c.readTimeout)
	case <-c.ctx.Done():
		c.deletePending(key)
		return nil, c.ctx.Err()
	}
}

func (c *appServerClient) notify(method string, params any) error {
	return c.send(map[string]any{"method": method, "params": params})
}

func (c *appServerClient) send(message any) error {
	b, err := json.Marshal(message)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.logs.WriteProtocol("send", bytes.TrimSuffix(b, []byte{'\n'}))
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.stdin.Write(b); err != nil {
		return fmt.Errorf("write codex JSON-RPC: %w", err)
	}
	return nil
}

func (c *appServerClient) scan(stdout io.Reader) {
	s := bufio.NewScanner(stdout)
	s.Buffer(make([]byte, 64*1024), 10*1024*1024)
	for s.Scan() {
		line := s.Text()
		c.logs.WriteProtocol("recv", []byte(line))
		var msg rpcMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			c.emit("malformed_message", line, "", "", domain.TokenUsage{}, nil)
			continue
		}
		if len(msg.ID) > 0 && msg.Method == "" {
			c.observeResponse(msg.Result)
			c.resolvePending(msg)
			continue
		}
		if msg.Method != "" {
			c.handleMessage(msg, line)
		}
	}
	err := fmt.Errorf("port_exit: codex app-server stdout closed")
	if scanErr := s.Err(); scanErr != nil {
		err = fmt.Errorf("read codex stdout: %w", scanErr)
	}
	c.failPending(err)
	c.finishTurn(turnResult{Err: err})
}

func (c *appServerClient) handleMessage(msg rpcMessage, line string) {
	method := msg.Method
	if method != "turn/completed" {
		c.updateSessionFromParams(msg.Params)
	}
	text, itemID := agentText(method, msg.Params)
	if len(msg.ID) > 0 {
		c.emit(c.eventType(method, msg.Params), line, text, itemID, ExtractUsage([]byte(line)), ExtractRateLimits([]byte(line)))
		go c.respondToServerRequest(msg)
		return
	}
	usage := ExtractUsage([]byte(line))
	if usage.TotalTokens != 0 {
		c.mu.Lock()
		c.usage = usage
		c.mu.Unlock()
	}
	c.emit(c.eventType(method, msg.Params), line, text, itemID, usage, ExtractRateLimits([]byte(line)))
	if method == "turn/completed" {
		c.finishTurn(c.turnCompletion(msg.Params))
	}
}

func (c *appServerClient) respondToServerRequest(msg rpcMessage) {
	method := msg.Method
	var result any
	var err *rpcError
	switch {
	case method == "mcpServer/elicitation/request" && c.shouldAutoApproveMCPToolCall(msg.Params):
		result = map[string]any{"action": "accept", "content": map[string]any{}}
	case method == "tool/requestUserInput" || method == "item/tool/requestUserInput" || method == "mcpServer/elicitation/request":
		err = &rpcError{Code: -32000, Message: "turn_input_required: Symphony does not provide interactive user input to autonomous runs"}
		c.finishTurn(turnResult{Err: fmt.Errorf("turn_input_required")})
	case method == "item/commandExecution/requestApproval" || method == "item/fileChange/requestApproval":
		result = map[string]any{"decision": "accept"}
	case method == "item/permissions/requestApproval":
		result = map[string]any{"permissions": rawParamField(msg.Params, "permissions", map[string]any{}), "scope": "turn", "strictAutoReview": false}
	case method == "item/tool/call":
		result = c.handleDynamicToolCall(msg.Params)
	default:
		err = &rpcError{Code: -32601, Message: "unsupported_tool_call: " + method}
	}
	if err != nil {
		_ = c.send(map[string]any{"id": msg.ID, "error": err})
		return
	}
	_ = c.send(map[string]any{"id": msg.ID, "result": result})
}

func (c *appServerClient) handleDynamicToolCall(params json.RawMessage) map[string]any {
	var root map[string]any
	if err := json.Unmarshal(params, &root); err != nil {
		return dynamicToolFailure("invalid tool call params")
	}
	toolName, _ := root["tool"].(string)
	switch toolName {
	case "beads_cli":
		args, ok := stringSliceField(root["arguments"], "args")
		if !ok || len(args) == 0 {
			return dynamicToolFailure("beads_cli requires args")
		}
		result := tools.ExecuteBeadsCLI(c.ctx, firstNonEmpty(c.req.TrackerWorkDir, c.req.Workspace), c.req.TrackerBDCommand, args)
		return dynamicToolResult(result)
	case "linear_graphql":
		query, variables, err := parseLinearGraphQLArgs(root["arguments"])
		if err != nil {
			return dynamicToolFailure(err.Error())
		}
		result := tools.ExecuteLinearGraphQL(c.ctx, c.req.TrackerEndpoint, c.req.TrackerAPIKey, query, variables)
		return dynamicToolResult(result)
	case "jira_rest":
		method, path, query, body, err := parseJiraRESTArgs(root["arguments"])
		if err != nil {
			return dynamicToolFailure(err.Error())
		}
		result := tools.ExecuteJiraREST(c.ctx, c.req.TrackerEndpoint, c.req.TrackerEmail, c.req.TrackerAPIKey, method, path, query, body)
		return dynamicToolResult(result)
	default:
		return dynamicToolFailure("unsupported_tool_call: " + firstNonEmpty(toolName, "unknown"))
	}
}

func dynamicToolResult(result tools.ToolResult) map[string]any {
	b, _ := json.Marshal(compactToolResult(result))
	return map[string]any{"success": result.Success, "contentItems": []map[string]any{{"type": "inputText", "text": string(b)}}}
}

func dynamicToolFailure(message string) map[string]any {
	b, _ := json.Marshal(map[string]any{"error": message})
	return map[string]any{"success": false, "contentItems": []map[string]any{{"type": "inputText", "text": string(b)}}}
}

func compactToolResult(result tools.ToolResult) map[string]any {
	out := map[string]any{"success": result.Success}
	if result.ExitCode != 0 {
		out["exit_code"] = result.ExitCode
	}
	if result.Error != "" {
		out["error"] = result.Error
	}
	if result.ParsedJSON != nil {
		if raw, err := json.Marshal(result.ParsedJSON); err == nil {
			if len(raw) <= dynamicToolPayloadMaxBytes {
				out["parsed_json"] = result.ParsedJSON
				return compactToolResultWithStderr(out, result)
			}
			out["parsed_json_preview"], _ = truncateToolText(string(raw), dynamicToolPayloadMaxBytes)
			out["truncated"] = true
			return compactToolResultWithStderr(out, result)
		}
	}
	if result.Stdout != "" {
		stdout, truncated := truncateToolText(result.Stdout, dynamicToolPayloadMaxBytes)
		out["stdout"] = stdout
		if truncated {
			out["truncated"] = true
		}
	}
	return compactToolResultWithStderr(out, result)
}

func compactToolResultWithStderr(out map[string]any, result tools.ToolResult) map[string]any {
	if result.Stderr != "" {
		stderr, truncated := truncateToolText(result.Stderr, dynamicToolPayloadMaxBytes)
		out["stderr"] = stderr
		if truncated {
			out["truncated"] = true
		}
	}
	if result.Truncated {
		out["truncated"] = true
	}
	return out
}

func truncateToolText(text string, maxBytes int) (string, bool) {
	if len(text) <= maxBytes {
		return text, false
	}
	for maxBytes > 0 && !utf8.ValidString(text[:maxBytes]) {
		maxBytes--
	}
	return text[:maxBytes] + dynamicToolTruncatedMarker, true
}

func parseLinearGraphQLArgs(v any) (string, map[string]any, error) {
	if s, ok := v.(string); ok {
		if strings.TrimSpace(s) == "" {
			return "", nil, fmt.Errorf("query must be non-empty")
		}
		return s, map[string]any{}, nil
	}
	root, ok := v.(map[string]any)
	if !ok || root == nil {
		return "", nil, fmt.Errorf("arguments must be an object or GraphQL query string")
	}
	query, ok := root["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return "", nil, fmt.Errorf("query must be non-empty")
	}
	variables := map[string]any{}
	if raw, ok := root["variables"]; ok {
		parsed, ok := raw.(map[string]any)
		if !ok {
			return "", nil, fmt.Errorf("variables must be an object")
		}
		variables = parsed
	}
	return query, variables, nil
}

func parseJiraRESTArgs(v any) (string, string, map[string]any, any, error) {
	root, ok := v.(map[string]any)
	if !ok || root == nil {
		return "", "", nil, nil, fmt.Errorf("arguments must be an object")
	}
	method, _ := root["method"].(string)
	path, _ := root["path"].(string)
	if strings.TrimSpace(method) == "" {
		return "", "", nil, nil, fmt.Errorf("method must be non-empty")
	}
	if strings.TrimSpace(path) == "" {
		return "", "", nil, nil, fmt.Errorf("path must be non-empty")
	}
	query := map[string]any{}
	if raw, ok := root["query"]; ok {
		parsed, ok := raw.(map[string]any)
		if !ok {
			return "", "", nil, nil, fmt.Errorf("query must be an object")
		}
		query = parsed
	}
	return method, path, query, root["body"], nil
}

func (c *appServerClient) shouldAutoApproveMCPToolCall(params json.RawMessage) bool {
	var root map[string]any
	if err := json.Unmarshal(params, &root); err != nil {
		return false
	}
	meta, _ := root["_meta"].(map[string]any)
	schema, _ := root["requestedSchema"].(map[string]any)
	required, _ := schema["required"].([]any)
	properties, _ := schema["properties"].(map[string]any)
	return fmt.Sprint(root["serverName"]) == "atlassian" &&
		meta["codex_approval_kind"] == "mcp_tool_call" &&
		len(required) == 0 &&
		len(properties) == 0
}

func (c *appServerClient) waitForTurn() turnResult {
	select {
	case res := <-c.turnDone:
		return res
	case <-c.ctx.Done():
		return turnResult{Err: c.ctx.Err()}
	}
}

func (c *appServerClient) turnCompletion(params json.RawMessage) turnResult {
	var root map[string]any
	if err := json.Unmarshal(params, &root); err != nil {
		return turnResult{Err: fmt.Errorf("turn_failed: invalid turn/completed params")}
	}
	turn, _ := root["turn"].(map[string]any)
	current := c.currentTurnID()
	if current == "" {
		return turnResult{}
	}
	if id := strValue(turn["id"]); id != "" && id != current {
		return turnResult{}
	}
	status, _ := turn["status"].(string)
	switch status {
	case "completed":
		return turnResult{Completed: true}
	case "interrupted":
		return turnResult{Err: fmt.Errorf("turn_cancelled")}
	default:
		msg := status
		if errObj, _ := turn["error"].(map[string]any); errObj != nil {
			if s, _ := errObj["message"].(string); s != "" {
				msg = s
			}
		}
		if msg == "" {
			msg = "unknown"
		}
		return turnResult{Err: fmt.Errorf("turn_failed: %s", msg)}
	}
}

func (c *appServerClient) eventType(method string, params json.RawMessage) string {
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval":
		return "approval_auto_approved"
	case "tool/requestUserInput", "item/tool/requestUserInput":
		return "turn_input_required"
	case "mcpServer/elicitation/request":
		if c.shouldAutoApproveMCPToolCall(params) {
			return "approval_auto_approved"
		}
		return "turn_input_required"
	case "turn/completed":
		res := c.turnCompletion(params)
		if res.Completed {
			return "turn_completed"
		}
		if res.Err != nil && strings.Contains(res.Err.Error(), "turn_cancelled") {
			return "turn_cancelled"
		}
		return "turn_failed"
	case "item/tool/call":
		return "tool_call"
	default:
		return strings.ReplaceAll(method, "/", "_")
	}
}

func agentText(method string, params json.RawMessage) (string, string) {
	var root map[string]any
	if err := json.Unmarshal(params, &root); err != nil {
		return "", ""
	}
	itemID := strValue(root["itemId"])
	if method == "item/completed" {
		item, _ := root["item"].(map[string]any)
		if item == nil || strValue(item["type"]) != "agentMessage" {
			return "", ""
		}
		itemID = strValue(item["id"])
		if text, ok := item["text"].(string); ok {
			return truncateAgentText(text), itemID
		}
		if content, ok := item["content"].(string); ok {
			return truncateAgentText(content), itemID
		}
		return "", itemID
	}
	if !strings.Contains(method, "agentMessage") && !strings.Contains(method, "assistant") {
		return "", ""
	}
	if item, ok := root["item"].(map[string]any); ok {
		if itemID == "" {
			itemID = strValue(item["id"])
		}
		if text, ok := item["text"].(string); ok {
			return truncateAgentText(text), itemID
		}
		if content, ok := item["content"].(string); ok {
			return truncateAgentText(content), itemID
		}
	}
	if text, ok := root["text"].(string); ok {
		return truncateAgentText(text), itemID
	}
	if delta, ok := root["delta"].(string); ok {
		return truncateAgentText(delta), itemID
	}
	return "", itemID
}

func truncateAgentText(text string) string {
	const max = 4000
	if len(text) <= max {
		return text
	}
	return text[:max]
}

func (c *appServerClient) emit(typeName, message, text, itemID string, usage domain.TokenUsage, rateLimits map[string]any) {
	if usage.TotalTokens != 0 {
		c.mu.Lock()
		c.usage = usage
		c.mu.Unlock()
	}
	sessionID, threadID, turnID := c.currentIdentity()
	select {
	case c.events <- agent.Event{SessionID: sessionID, ThreadID: threadID, TurnID: turnID, IssueID: c.req.Issue.ID, ItemID: itemID, Type: typeName, Message: message, Text: text, Usage: usage, RateLimits: rateLimits, At: time.Now()}:
	case <-c.ctx.Done():
	}
}

func (c *appServerClient) result(err error, completed bool) agent.Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	res := agent.Result{
		SessionID: c.sessionID,
		ThreadID:  c.threadID,
		TurnID:    c.turnID,
		Usage:     c.usage,
		Logs:      c.req.Logs,
		Err:       err,
		Completed: completed,
	}
	c.logs.WriteResult(res)
	return res
}

func (c *appServerClient) setTurn(threadID, turnID string) {
	c.setObservedIDs(threadID, turnID)
}

func (c *appServerClient) prepareTurn(threadID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.threadID = threadID
	c.turnID = ""
	c.started = false
	c.turnDone = make(chan turnResult, 1)
}

func (c *appServerClient) observeResponse(result json.RawMessage) {
	threadID := nestedID(result, "thread")
	turnID := nestedID(result, "turn")
	c.setObservedIDs(threadID, turnID)
}

func (c *appServerClient) updateSessionFromParams(params json.RawMessage) {
	var root map[string]any
	if err := json.Unmarshal(params, &root); err != nil {
		return
	}
	threadID := strValue(root["threadId"])
	if threadID == "" {
		thread, _ := root["thread"].(map[string]any)
		threadID = strValue(thread["id"])
	}
	turnID := strValue(root["turnId"])
	if turnID == "" {
		turn, _ := root["turn"].(map[string]any)
		turnID = strValue(turn["id"])
	}
	if threadID == "" && turnID == "" {
		return
	}
	c.setObservedIDs(threadID, turnID)
}

func (c *appServerClient) setObservedIDs(threadID, turnID string) {
	started := false
	c.mu.Lock()
	if threadID != "" {
		c.threadID = threadID
	}
	if turnID != "" {
		c.turnID = turnID
	}
	if c.threadID != "" && c.turnID != "" {
		c.sessionID = c.threadID + "-" + c.turnID
		if !c.started {
			c.started = true
			started = true
		}
	}
	c.mu.Unlock()
	if started {
		c.emit("session_started", "turn started", "", "", domain.TokenUsage{}, nil)
	}
}

func (c *appServerClient) currentIdentity() (string, string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID, c.threadID, c.turnID
}

func (c *appServerClient) currentTurnID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turnID
}

func (c *appServerClient) nextRequestID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	return id
}

func (c *appServerClient) resolvePending(msg rpcMessage) {
	key := string(msg.ID)
	c.mu.Lock()
	ch := c.pending[key]
	delete(c.pending, key)
	c.mu.Unlock()
	if ch != nil {
		ch <- msg
	}
}

func (c *appServerClient) deletePending(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, key)
}

func (c *appServerClient) failPending(err error) {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[string]chan rpcMessage{}
	c.mu.Unlock()
	msg := rpcMessage{Error: &rpcError{Code: -32000, Message: err.Error()}}
	for _, ch := range pending {
		ch <- msg
	}
}

func (c *appServerClient) finishTurn(res turnResult) {
	if res.Err == nil && !res.Completed {
		return
	}
	select {
	case c.turnDone <- res:
	default:
	}
}

func nestedID(raw json.RawMessage, key string) string {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return ""
	}
	child, _ := root[key].(map[string]any)
	id, _ := child["id"].(string)
	return id
}

func policyMap(policy any) map[string]any {
	if policy == nil {
		return nil
	}
	if m, ok := policy.(map[string]any); ok {
		return m
	}
	return nil
}

func rawParamField(params json.RawMessage, key string, fallback any) any {
	var root map[string]any
	if err := json.Unmarshal(params, &root); err != nil {
		return fallback
	}
	if v, ok := root[key]; ok {
		return v
	}
	return fallback
}

func stringSliceField(v any, key string) ([]string, bool) {
	root, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	raw, ok := root[key].([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func strValue(v any) string {
	s, _ := v.(string)
	return s
}

func extractThreadTurnID(line string) (string, string) {
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return "", ""
	}
	params, _ := m["params"].(map[string]any)
	threadID, _ := params["threadId"].(string)
	turn, _ := params["turn"].(map[string]any)
	turnID, _ := turn["id"].(string)
	if threadID == "" {
		thread, _ := params["thread"].(map[string]any)
		threadID, _ = thread["id"].(string)
	}
	return threadID, turnID
}

func strField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
