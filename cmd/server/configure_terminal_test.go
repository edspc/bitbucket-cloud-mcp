//go:build darwin || linux

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestTerminalLineProcess(t *testing.T) {
	mode := os.Getenv("BITBUCKET_MCP_TERMINAL_TEST")
	if mode == "" {
		t.Skip("terminal subprocess helper")
	}
	value, err := terminalLine(os.Stdin, os.Stdout, "TOKEN: ", true)
	if mode == "interrupt" {
		if err == nil {
			t.Fatal("interrupt did not cancel input")
		}
	} else if err != nil || value != "fake-review-token" {
		t.Fatal("could not read hidden token")
	}
	if _, err := io.WriteString(os.Stdout, "READ_DONE\n"); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalTokenIsHiddenAndEchoRestored(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required for the terminal regression test")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-c", `
import os, pty, select, signal, subprocess, sys, termios, time
for mode in ("success", "interrupt"):
    master, slave = pty.openpty()
    original = termios.tcgetattr(slave)
    env = dict(os.environ, BITBUCKET_MCP_TERMINAL_TEST=mode)
    proc = subprocess.Popen([sys.argv[1], "-test.run=^TestTerminalLineProcess$"],
                            stdin=slave, stdout=slave, stderr=slave, env=env)
    try:
        output = b""
        deadline = time.monotonic() + 10
        while b"TOKEN: " not in output:
            assert time.monotonic() < deadline, "token prompt timed out"
            if select.select([master], [], [], 0.1)[0]:
                output += os.read(master, 4096)
        assert not termios.tcgetattr(slave)[3] & termios.ECHO, "echo enabled when token prompt appeared"
        if mode == "success":
            os.write(master, b"fake-review-token\n")
        else:
            proc.send_signal(signal.SIGINT)
        while proc.poll() is None:
            assert time.monotonic() < deadline, "terminal input timed out"
            if select.select([master], [], [], 0.1)[0]:
                output += os.read(master, 4096)
        while select.select([master], [], [], 0)[0]:
            output += os.read(master, 4096)
        assert proc.returncode == 0, output
        assert b"READ_DONE" in output, output
        assert b"fake-review-token" not in output, "token was echoed"
        assert termios.tcgetattr(slave) == original, "terminal settings were not restored"
    finally:
        if proc.poll() is None:
            proc.kill()
        proc.wait()
        os.close(master)
        os.close(slave)
`, binary)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("terminal regression: %v\n%s", err, output)
	}
}
