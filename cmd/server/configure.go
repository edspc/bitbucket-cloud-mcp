package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
)

func promptConfiguration(out io.Writer, readLine func(string, bool) (string, error)) (map[string]string, error) {
	if _, err := fmt.Fprintln(out, "Configure macOS Keychain (replaces saved settings).\nOptional limits: enter a JSON array, or leave blank for the default.\nFor repositories, projects and refs, [] denies all. For tools, [] allows all.\nConfirmation tools: blank or [] confirms writes; [\"*\"] confirms every tool."); err != nil {
		return nil, err
	}
	values := make(map[string]string)
	for _, field := range []struct {
		name, label      string
		secret, required bool
	}{
		{"BITBUCKET_WORKSPACE", "Workspace slug", false, true},
		{"BITBUCKET_EMAIL", "Atlassian email", false, true},
		{"BITBUCKET_API_TOKEN", "API token (hidden)", true, true},
		{"BITBUCKET_ALLOWED_REPOS", "Allowed repositories", false, false},
		{"BITBUCKET_ALLOWED_PROJECTS", "Allowed projects", false, false},
		{"BITBUCKET_ALLOWED_TOOLS", "Allowed tools", false, false},
		{"BITBUCKET_ALLOWED_REFS", "Allowed refs (branch:name or tag:name)", false, false},
		{"BITBUCKET_CONFIRM_TOOLS", "Tools requiring confirmation", false, false},
		{"BITBUCKET_ENABLE_API_PROXY", "Enable API proxy (true/false; blank disables)", false, false},
		{"BITBUCKET_LOCAL_BROWSER", "Open approval pages in system browser (true/false; blank uses MCP)", false, false},
	} {
		for {
			value, err := readLine(field.label+": ", field.secret)
			if field.secret {
				if _, err := fmt.Fprintln(out); err != nil {
					return nil, err
				}
			}
			if err != nil {
				return nil, errors.New("configuration input interrupted; settings were not saved")
			}
			if strings.TrimSpace(value) == "" {
				if !field.required {
					break
				}
				if _, err = fmt.Fprintln(out, "A value is required."); err != nil {
					return nil, err
				}
				continue
			}
			values[field.name] = value
			if !field.required {
				lookup := func(name string) (string, bool) { value, set := values[name]; return value, set }
				if _, err = policyFromLookup(lookup); err != nil {
					delete(values, field.name)
					if _, err = fmt.Fprintln(out, err); err != nil {
						return nil, err
					}
					continue
				}
			}
			break
		}
	}
	return validateConfiguration(values)
}

func terminalLine(terminal *os.File, out io.Writer, prompt string, secret bool) (value string, err error) {
	signals := make(chan os.Signal, 1)
	if secret {
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(signals)
		state := exec.Command("/bin/stty", "-g")
		state.Stdin = terminal
		var data []byte
		data, err = state.Output()
		if err != nil {
			return "", errors.New("cannot read terminal settings")
		}
		disable := exec.Command("/bin/stty", "-echo")
		disable.Stdin = terminal
		if err = disable.Run(); err != nil {
			return "", errors.New("cannot hide token input")
		}
		defer func() {
			restore := exec.Command("/bin/stty", strings.TrimSpace(string(data)))
			restore.Stdin = terminal
			if restore.Run() != nil {
				err = errors.New("cannot restore terminal settings")
			}
		}()
	}
	if _, err = io.WriteString(out, prompt); err != nil {
		return "", err
	}
	type result struct {
		value string
		err   error
	}
	done := make(chan result, 1)
	go func() { value, err := readTerminalLine(terminal); done <- result{value, err} }()
	select {
	case result := <-done:
		return result.value, result.err
	case <-signals:
		return "", errors.New("configuration cancelled")
	}
}

func readTerminalLine(in io.Reader) (string, error) {
	// Do not buffer future lines before token echo has been disabled.
	var line strings.Builder
	var b [1]byte
	for {
		_, err := io.ReadFull(in, b[:])
		if err != nil {
			return "", err
		}
		if b[0] == '\n' {
			return strings.TrimSuffix(line.String(), "\r"), nil
		}
		line.WriteByte(b[0])
	}
}
