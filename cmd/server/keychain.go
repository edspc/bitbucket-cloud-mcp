package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func configurationLookup() (func(string) (string, bool), error) {
	if runtime.GOOS != "darwin" {
		return mergedLookup(nil, os.LookupEnv), nil
	}
	return configurationLookupForKeychain("")
}

func configurationLookupForKeychain(keychain string) (func(string) (string, bool), error) {
	values, err := loadKeychain(keychain)
	if err != nil {
		return nil, err
	}
	return mergedLookup(values, os.LookupEnv), nil
}

func loadKeychain(keychain string) (map[string]string, error) {
	if err := checkKeychainPath(keychain); err != nil {
		return nil, err
	}
	args := []string{"find-generic-password", "-s", "bitbucket-cloud-mcp", "-a", "configuration", "-w"}
	if keychain != "" {
		args = append(args, keychain)
	}
	data, err := exec.Command("/usr/bin/security", args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 44 {
			return map[string]string{}, nil
		}
		return nil, errors.New("cannot read macOS Keychain configuration")
	}
	var values map[string]string
	if err = json.Unmarshal(data, &values); err != nil || values == nil {
		return nil, errors.New("invalid macOS Keychain configuration")
	}
	return values, nil
}

func checkKeychainPath(keychain string) error {
	if keychain == "" {
		return nil
	}
	// security can report a missing Keychain file as a missing item.
	info, err := os.Stat(keychain)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("cannot open macOS Keychain")
	}
	return nil
}

func mergedLookup(values map[string]string, env func(string) (string, bool)) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if value, set := env(name); set {
			return value, true
		}
		value, set := values[name]
		return value, set
	}
}

func validateConfiguration(values map[string]string) (map[string]string, error) {
	for name := range values {
		switch name {
		case "BITBUCKET_WORKSPACE", "BITBUCKET_EMAIL", "BITBUCKET_API_TOKEN", "BITBUCKET_ALLOWED_REPOS", "BITBUCKET_ALLOWED_PROJECTS", "BITBUCKET_ALLOWED_TOOLS", "BITBUCKET_ALLOWED_REFS", "BITBUCKET_CONFIRM_TOOLS", "BITBUCKET_ENABLE_API_PROXY":
		default:
			return nil, errors.New("unknown configuration parameter")
		}
	}
	lookup := func(name string) (string, bool) { value, set := values[name]; return value, set }
	if _, err := configFromLookup(lookup); err != nil {
		return nil, err
	}
	if _, err := policyFromLookup(lookup); err != nil {
		return nil, err
	}
	return values, nil
}

func keychainWriteCommand(values map[string]string, keychain string) (string, error) {
	data, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	// security's interactive parser accepts double-quoted arguments with backslash escapes.
	password := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(string(data))
	command := "add-generic-password -U -s bitbucket-cloud-mcp -a configuration -w \"" + password + "\""
	if keychain != "" {
		if strings.ContainsAny(keychain, "\r\n\x00") {
			return "", errors.New("invalid Keychain path")
		}
		command += " \"" + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(keychain) + "\""
	}
	// security -i reads at most 4095 bytes per line, including the command and its arguments.
	if len(command) > 4095 {
		return "", errors.New("configuration exceeds the macOS Keychain command size limit; saved settings were not changed")
	}
	return command + "\n", nil
}

func configure(in io.Reader, out io.Writer) error {
	if runtime.GOOS != "darwin" {
		return errors.New("configure requires macOS Keychain")
	}
	terminal, ok := in.(*os.File)
	if !ok {
		return errors.New("configure requires an interactive terminal")
	}
	check := exec.Command("/bin/stty", "-g")
	check.Stdin = terminal
	if _, err := check.Output(); err != nil {
		return errors.New("configure requires an interactive terminal")
	}
	values, err := promptConfiguration(out, func(prompt string, secret bool) (string, error) {
		return terminalLine(terminal, out, prompt, secret)
	})
	if err != nil {
		return err
	}
	if err = saveKeychain(values, ""); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Configuration saved to macOS Keychain.")
	return err
}

func saveKeychain(values map[string]string, keychain string) error {
	if _, err := validateConfiguration(values); err != nil {
		return err
	}
	if err := checkKeychainPath(keychain); err != nil {
		return err
	}
	command, err := keychainWriteCommand(values, keychain)
	if err != nil {
		return err
	}
	// Keep secrets out of process arguments and suppress security's command output.
	cmd := exec.Command("/usr/bin/security", "-i")
	cmd.Stdin = strings.NewReader(command)
	if err = cmd.Run(); err != nil {
		return errors.New("cannot save macOS Keychain configuration")
	}
	saved, err := loadKeychain(keychain)
	if err != nil {
		return err
	}
	if !maps.Equal(saved, values) {
		return errors.New("macOS Keychain did not save the configuration")
	}
	return nil
}
