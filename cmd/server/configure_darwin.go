//go:build !ios

package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

type configurationField struct {
	name, label      string
	secret, required bool
}

func configurationFields() []configurationField {
	return []configurationField{
		{"BITBUCKET_WORKSPACE", "Workspace slug", false, true},
		{"BITBUCKET_EMAIL", "Atlassian email", false, true},
		{"BITBUCKET_API_TOKEN", "API token (hidden)", true, true},
		{"BITBUCKET_ALLOWED_REPOS", "Allowed repositories", false, false},
		{"BITBUCKET_ALLOWED_PROJECTS", "Allowed projects", false, false},
		{"BITBUCKET_ALLOWED_TOOLS", "Allowed tools (tool, tool@repo, !tool@repo)", false, false},
		{"BITBUCKET_ALLOWED_REFS", "Allowed refs (branch:name or tag:name)", false, false},
		{"BITBUCKET_CONFIRM_TOOLS", "Tools requiring confirmation", false, false},
		{"BITBUCKET_ENABLE_API_PROXY", "Enable API proxy (true/false)", false, false},
		{"BITBUCKET_LOCAL_BROWSER", "Open approval pages in system browser (true/false)", false, false},
	}
}

func promptConfigurationEdit(out io.Writer, readLine func(string, bool) (string, error), saved map[string]string) (map[string]string, error) {
	if len(saved) == 0 {
		return promptConfiguration(out, readLine)
	}
	if _, err := fmt.Fprintln(out, "Edit macOS Keychain configuration. Other saved settings are preserved."); err != nil {
		return nil, err
	}
	fields := configurationFields()
	for i, field := range fields {
		if _, err := fmt.Fprintf(out, "%d. %s\n", i+1, field.label); err != nil {
			return nil, err
		}
	}
	if _, err := fmt.Fprintln(out, "all. Configure all settings (replace saved configuration)\n0. Cancel"); err != nil {
		return nil, err
	}
	for {
		choice, err := readLine("Setting to change (number, parameter name, all or 0): ", false)
		if err != nil || strings.TrimSpace(choice) == "0" {
			return nil, errors.New("configuration cancelled; settings were not saved")
		}
		choice = strings.TrimSpace(choice)
		if choice == "all" {
			return promptConfiguration(out, readLine)
		}
		for i, field := range fields {
			if choice == strconv.Itoa(i+1) || choice == field.name {
				return promptConfigurationField(out, readLine, saved, field)
			}
		}
		if _, err = fmt.Fprintln(out, "Choose a listed setting, all or 0."); err != nil {
			return nil, err
		}
	}
}

func promptConfigurationField(out io.Writer, readLine func(string, bool) (string, error), saved map[string]string, field configurationField) (map[string]string, error) {
	if !field.secret {
		current, set := saved[field.name]
		if !set {
			current = "(default)"
		}
		if _, err := fmt.Fprintf(out, "Current value: %s\n", current); err != nil {
			return nil, err
		}
	}
	help := "Leave blank to keep the saved value."
	if !field.required {
		help += " Enter /clear to restore the default."
	}
	if _, err := fmt.Fprintln(out, help); err != nil {
		return nil, err
	}
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
		values := maps.Clone(saved)
		if strings.TrimSpace(value) != "" {
			if !field.required && value == "/clear" {
				delete(values, field.name)
			} else {
				values[field.name] = value
			}
		}
		if _, err = validateConfiguration(values); err == nil {
			return values, nil
		}
		if _, err = fmt.Fprintln(out, err); err != nil {
			return nil, err
		}
	}
}

func promptConfiguration(out io.Writer, readLine func(string, bool) (string, error)) (map[string]string, error) {
	if _, err := fmt.Fprintln(out, "Configure macOS Keychain (replaces saved settings).\nOptional limits: enter a JSON array, or leave blank for the default.\nFor repositories, projects and refs, [] denies all. For tools, [] allows all.\nConfirmation tools: blank or [] confirms writes; [\"*\"] confirms every tool."); err != nil {
		return nil, err
	}
	values := make(map[string]string)
	for _, field := range configurationFields() {
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
