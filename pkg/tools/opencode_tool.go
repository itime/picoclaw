package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type OpenCodeTool struct {
	workDir     string
	opencodeBin string
	timeout     time.Duration
}

func NewOpenCodeTool(workDir string) *OpenCodeTool {
	bin := "/Users/lzw/.bun/bin/opencode"
	return &OpenCodeTool{
		workDir:     workDir,
		opencodeBin: bin,
		timeout:     10 * time.Minute,
	}
}

func (t *OpenCodeTool) Name() string {
	return "opencode"
}

func (t *OpenCodeTool) Description() string {
	return "Execute coding tasks using opencode AI coding assistant. Use this to write code, fix bugs, refactor, or implement features in the local codebase."
}

func (t *OpenCodeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task": map[string]interface{}{
				"type":        "string",
				"description": "The coding task to execute. Be specific about what to implement, fix, or change.",
			},
			"working_directory": map[string]interface{}{
				"type":        "string",
				"description": "The directory to run opencode in. Defaults to the configured workspace.",
			},
		},
		"required": []string{"task"},
	}
}

func (t *OpenCodeTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	task, ok := args["task"].(string)
	if !ok || task == "" {
		return ErrorResult("task parameter is required")
	}

	workDir := t.workDir
	if wd, ok := args["working_directory"].(string); ok && wd != "" {
		workDir = wd
	}

	execCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	// opencode run [message] --dir [workDir]
	cmd := exec.CommandContext(execCtx, t.opencodeBin, "run", task, "--dir", workDir)
	cmd.Dir = workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\n\nStderr:\n" + stderr.String()
	}

	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return ErrorResult(fmt.Sprintf("opencode timed out after %v", t.timeout))
		}
		return &ToolResult{
			ForLLM:  fmt.Sprintf("opencode completed with error: %v\n\nOutput:\n%s", err, output),
			IsError: false,
		}
	}

	if output == "" {
		output = "opencode completed successfully (no output)"
	}

	if len(output) > 4000 {
		output = output[:2000] + "\n\n... (truncated) ...\n\n" + output[len(output)-1500:]
	}

	return &ToolResult{
		ForLLM:  output,
		IsError: false,
	}
}

func (t *OpenCodeTool) SetWorkDir(dir string) {
	t.workDir = dir
}

func (t *OpenCodeTool) SetTimeout(d time.Duration) {
	t.timeout = d
}

func (t *OpenCodeTool) SetOpenCodeBin(bin string) {
	t.opencodeBin = bin
}

func truncateOutput(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	half := maxLen / 2
	return s[:half] + "\n\n... (truncated) ...\n\n" + s[len(s)-half:]
}

func init() {
	_ = strings.TrimSpace
}
