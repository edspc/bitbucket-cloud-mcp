package tools

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
)

func openSystemBrowser(ctx context.Context, target string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(ctx, "/usr/bin/open", target).Run()
	case "linux":
		return exec.CommandContext(ctx, "xdg-open", target).Run()
	}
	return errors.New("system browser is unsupported on this platform")
}
