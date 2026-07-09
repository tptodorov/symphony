package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
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

var toolHTTPClient = &http.Client{Timeout: 30 * time.Second}

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
	if !exactlyOneGraphQLOperation(query) {
		return ToolResult{Success: false, Error: "query must contain exactly one GraphQL operation"}
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
	resp, err := toolHTTPClient.Do(req)
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

func exactlyOneGraphQLOperation(query string) bool {
	cleaned := stripGraphQLIgnored(query)
	depth := 0
	count := 0
	skipNextTopLevelSelection := false
	for i := 0; i < len(cleaned); {
		ch := cleaned[i]
		if ch == '{' {
			if depth == 0 {
				if skipNextTopLevelSelection {
					skipNextTopLevelSelection = false
				} else {
					count++
				}
			}
			depth++
			i++
			continue
		}
		if ch == '}' {
			if depth > 0 {
				depth--
			}
			i++
			continue
		}
		if depth == 0 && isGraphQLNameStart(ch) {
			start := i
			i++
			for i < len(cleaned) && isGraphQLNameContinue(cleaned[i]) {
				i++
			}
			token := cleaned[start:i]
			if token == "query" || token == "mutation" || token == "subscription" {
				count++
				skipNextTopLevelSelection = true
			} else if token == "fragment" {
				skipNextTopLevelSelection = true
			}
			continue
		}
		i++
	}
	return count == 1
}

func stripGraphQLIgnored(query string) string {
	var out strings.Builder
	for i := 0; i < len(query); {
		switch query[i] {
		case '#':
			for i < len(query) && query[i] != '\n' {
				i++
			}
		case '"':
			if strings.HasPrefix(query[i:], `"""`) {
				i += 3
				for i < len(query) && !strings.HasPrefix(query[i:], `"""`) {
					i++
				}
				if i < len(query) {
					i += 3
				}
				continue
			}
			i++
			for i < len(query) {
				if query[i] == '\\' {
					i += 2
					continue
				}
				if query[i] == '"' {
					i++
					break
				}
				i++
			}
		default:
			out.WriteByte(query[i])
			i++
		}
	}
	return out.String()
}

func isGraphQLNameStart(ch byte) bool {
	return ch == '_' || (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')
}

func isGraphQLNameContinue(ch byte) bool {
	return isGraphQLNameStart(ch) || (ch >= '0' && ch <= '9')
}

func ExecuteJiraREST(ctx context.Context, endpoint, email, apiToken, method, path string, query map[string]any, body any) ToolResult {
	if endpoint == "" {
		return ToolResult{Success: false, Error: "tracker.endpoint is not configured"}
	}
	if email == "" {
		return ToolResult{Success: false, Error: "tracker.email is not configured"}
	}
	if apiToken == "" {
		return ToolResult{Success: false, Error: "tracker.api_token is not configured"}
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if !allowedJiraMethod(method) {
		return ToolResult{Success: false, Error: "method must be GET, POST, PUT, PATCH, or DELETE"}
	}
	if !strings.HasPrefix(path, "/rest/api/") || strings.Contains(path, "://") || strings.HasPrefix(path, "//") {
		return ToolResult{Success: false, Error: "path must be a relative Jira REST path beginning with /rest/api/"}
	}
	u, err := url.Parse(strings.TrimRight(endpoint, "/") + path)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	values, err := jiraQueryValues(query)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	u.RawQuery = values.Encode()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return ToolResult{Success: false, Error: err.Error()}
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetBasicAuth(email, apiToken)
	resp, err := toolHTTPClient.Do(req)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	defer resp.Body.Close()
	var respBody bytes.Buffer
	_, _ = io.Copy(&respBody, resp.Body)
	result := ToolResult{Success: resp.StatusCode >= 200 && resp.StatusCode < 300, Stdout: respBody.String(), ExitCode: resp.StatusCode}
	if !result.Success {
		result.Error = fmt.Sprintf("jira non-2xx status: %d", resp.StatusCode)
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "json") || json.Valid(respBody.Bytes()) {
		var parsed any
		if err := json.Unmarshal(respBody.Bytes(), &parsed); err == nil {
			result.ParsedJSON = parsed
		}
	}
	return result
}

func allowedJiraMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func jiraQueryValues(query map[string]any) (url.Values, error) {
	values := url.Values{}
	for key, value := range query {
		switch v := value.(type) {
		case nil:
			values.Add(key, "")
		case string:
			values.Add(key, v)
		case bool, float64, int, int64:
			values.Add(key, fmt.Sprint(v))
		case []any:
			for _, item := range v {
				switch x := item.(type) {
				case nil:
					values.Add(key, "")
				case string:
					values.Add(key, x)
				case bool, float64, int, int64:
					values.Add(key, fmt.Sprint(x))
				default:
					return nil, fmt.Errorf("query %s contains unsupported array value", key)
				}
			}
		default:
			return nil, fmt.Errorf("query %s has unsupported value", key)
		}
	}
	return values, nil
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
