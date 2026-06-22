package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"time"
)

type ToolResult struct {
	Success    bool
	Stdout     string
	Stderr     string
	ExitCode   int
	ParsedJSON any
	Error      string
	Truncated  bool
}

func ExecuteBeadsCLI(ctx context.Context, workDir, bdCmd string, args []string) ToolResult {
	if bdCmd == "" {
		return ToolResult{Success: false, Error: "tracker.bd_command is not configured"}
	}
	if len(args) == 0 {
		return ToolResult{Success: false, Error: "args must not be empty"}
	}
	sh := bdCmd
	for _, a := range args {
		sh += " " + fmt.Sprintf("%q", a)
	}
	cmd := exec.CommandContext(ctx, "bash", "-lc", sh)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Env = append(cmd.Environ(), "BD_JSON_ENVELOPE=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	err := cmd.Run()
	dur := time.Since(start)
	if dur > 5*time.Second {
		truncateOutput(&stdout, &stderr)
	}
	exitCode := 0
	if err != nil {
		exitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	result := ToolResult{
		Success:  err == nil,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}
	if err != nil {
		result.Error = err.Error()
	}
	if args[len(args)-1] == "--json" || containsJSON(args) {
		var parsed any
		if json.Unmarshal(stdout.Bytes(), &parsed) == nil {
			result.ParsedJSON = parsed
		}
	}
	return result
}

func ExecuteLinearGraphQL(ctx context.Context, endpoint, apiKey string, query string, variables map[string]any) ToolResult {
	if endpoint == "" {
		return ToolResult{Success: false, Error: "tracker.endpoint is not configured"}
	}
	if apiKey == "" {
		return ToolResult{Success: false, Error: "tracker.api_key is not configured"}
	}
	if query == "" {
		return ToolResult{Success: false, Error: "query must not be empty"}
	}
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", apiKey)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	defer resp.Body.Close()
	var respBody bytes.Buffer
	_, _ = io.Copy(&respBody, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return ToolResult{Success: false, Stdout: respBody.String(), Error: fmt.Sprintf("linear non-200 status: %d", resp.StatusCode)}
	}
	var env struct {
		Data   json.RawMessage  `json:"data"`
		Errors []map[string]any `json:"errors"`
	}
	if err := json.Unmarshal(respBody.Bytes(), &env); err != nil {
		return ToolResult{Success: false, Stdout: respBody.String(), Error: err.Error()}
	}
	result := ToolResult{Success: len(env.Errors) == 0}
	if len(env.Errors) > 0 {
		result.Error = fmt.Sprintf("graphql errors: %v", env.Errors)
	}
	result.ParsedJSON = json.RawMessage(env.Data)
	result.Stdout = respBody.String()
	return result
}

func containsJSON(args []string) bool {
	for _, a := range args {
		if a == "--json" {
			return true
		}
	}
	return false
}

func truncateOutput(stdout, stderr *bytes.Buffer) {
	const max = 4096
	if stdout.Len() > max {
		stdout.Truncate(max)
		stdout.WriteString("...")
	}
	if stderr.Len() > max {
		stderr.Truncate(max)
		stderr.WriteString("...")
	}
}
