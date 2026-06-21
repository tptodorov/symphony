package workflow

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/openai/symphony/go/internal/domain"
	"gopkg.in/yaml.v3"
)

var (
	ErrMissingWorkflowFile       = errors.New("missing_workflow_file")
	ErrWorkflowParse             = errors.New("workflow_parse_error")
	ErrWorkflowFrontMatterNotMap = errors.New("workflow_front_matter_not_a_map")
)

func Load(path string) (domain.WorkflowDefinition, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.WorkflowDefinition{}, ErrMissingWorkflowFile
		}
		return domain.WorkflowDefinition{}, fmt.Errorf("read workflow: %w", err)
	}
	text := string(b)
	if !strings.HasPrefix(text, "---") {
		return domain.WorkflowDefinition{Config: map[string]any{}, PromptTemplate: strings.TrimSpace(text)}, nil
	}
	rest := text[3:]
	if strings.HasPrefix(rest, "\r\n") {
		rest = rest[2:]
	} else if strings.HasPrefix(rest, "\n") {
		rest = rest[1:]
	}
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return domain.WorkflowDefinition{}, ErrWorkflowParse
	}
	yamlText := rest[:idx]
	body := rest[idx+4:]
	if strings.HasPrefix(body, "\r\n") {
		body = body[2:]
	} else if strings.HasPrefix(body, "\n") {
		body = body[1:]
	}
	cfg := map[string]any{}
	if strings.TrimSpace(yamlText) != "" {
		var v any
		if err := yaml.Unmarshal([]byte(yamlText), &v); err != nil {
			return domain.WorkflowDefinition{}, fmt.Errorf("%w: %v", ErrWorkflowParse, err)
		}
		m, ok := normalizeMap(v).(map[string]any)
		if !ok {
			return domain.WorkflowDefinition{}, ErrWorkflowFrontMatterNotMap
		}
		cfg = m
	}
	return domain.WorkflowDefinition{Config: cfg, PromptTemplate: strings.TrimSpace(body)}, nil
}

func normalizeMap(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := map[string]any{}
		for k, v := range x {
			m[k] = normalizeMap(v)
		}
		return m
	case map[any]any:
		m := map[string]any{}
		for k, v := range x {
			ks, ok := k.(string)
			if !ok {
				continue
			}
			m[ks] = normalizeMap(v)
		}
		return m
	case []any:
		for i := range x {
			x[i] = normalizeMap(x[i])
		}
	}
	return v
}
