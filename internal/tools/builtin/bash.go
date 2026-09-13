package builtin

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/youngyangyang04/numbat/internal/tools"
)

const (
	// bashMaxOutputBytes 是输出截断阈值。
	bashMaxOutputBytes = 64 * 1024
	// bashDefaultTimeout 是默认超时秒数。
	bashDefaultTimeout = 60
	// bashMaxTimeout 是允许的最大超时秒数。
	bashMaxTimeout = 120
)

// BashTool 执行 shell 命令。
type BashTool struct{}

// Name 返回工具名。
func (BashTool) Name() string { return "bash" }

// Description 返回工具描述。
func (BashTool) Description() string {
	return "Execute a shell command and return its output (stdout + stderr combined). " +
		"Non-interactive only — commands requiring user input will hang and time out. " +
		"Prefer short, focused commands. Output is truncated at 64 KB."
}

// InputSchema 返回参数 schema。
func (BashTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "Shell command to execute.",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": fmt.Sprintf("Maximum seconds to wait (default %d, max %d).", bashDefaultTimeout, bashMaxTimeout),
				"minimum":     1,
				"maximum":     bashMaxTimeout,
			},
		},
		"required": []string{"command"},
	}
}

// Invoke 执行工具：合并 stdout/stderr，超时或非零退出码返回错误。
func (BashTool) Invoke(ctx context.Context, params map[string]any) (tools.Result, error) {
	command, _ := params["command"].(string)
	if command == "" {
		return tools.Result{Content: "command is required", IsError: true, ErrorType: "schema_error"}, nil
	}

	timeout := bashDefaultTimeout
	if n, ok := params["timeout"].(float64); ok && n > 0 {
		timeout = min(int(n), bashMaxTimeout)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	shell, arg := "/bin/sh", "-c"
	if runtime.GOOS == "windows" {
		shell, arg = "cmd", "/c"
	}

	cmd := exec.CommandContext(ctx, shell, arg, command)
	// 超时只 terminate 直接子进程；若命令 fork 出持有输出管道的后台进程，
	// CombinedOutput 会一直等管道 EOF。WaitDelay 到点后强制关闭管道并返回。
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return tools.Result{Content: fmt.Sprintf("[timeout after %ds]", timeout), IsError: true, ErrorType: "timeout"}, nil
	}

	output := string(out)
	if len(output) > bashMaxOutputBytes {
		output = output[:bashMaxOutputBytes] + "\n[truncated]"
	}

	if err != nil {
		return tools.Result{
			Content:   fmt.Sprintf("[exit %d]\n%s", exitCode(err), output),
			IsError:   true,
			ErrorType: "runtime_error",
		}, nil
	}
	if output == "" {
		output = "[no output]"
	}
	return tools.Result{Content: output}, nil
}

// exitCode 提取进程退出码；命令未能启动时回退为 1。
func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}
