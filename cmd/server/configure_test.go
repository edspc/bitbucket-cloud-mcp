package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestInteractiveConfiguration(t *testing.T) {
	lines := []string{"", "team", "email", "private-token", "null", "[]", "", `["get_pipeline"]`, `["branch:main"]`, `["*"]`, "false", "true"}
	var out bytes.Buffer
	var hidden []bool
	values, err := promptConfiguration(&out, func(prompt string, secret bool) (string, error) {
		if _, err := io.WriteString(&out, prompt); err != nil {
			return "", err
		}
		hidden = append(hidden, secret)
		if len(lines) == 0 {
			return "", io.EOF
		}
		value := lines[0]
		lines = lines[1:]
		return value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if values["BITBUCKET_WORKSPACE"] != "team" || values["BITBUCKET_API_TOKEN"] != "private-token" || values["BITBUCKET_ALLOWED_REPOS"] != "[]" {
		t.Fatal("incorrect answers saved")
	}
	if _, set := values["BITBUCKET_ALLOWED_PROJECTS"]; set {
		t.Fatal("blank optional answer must be unset")
	}
	if len(hidden) != 12 || !hidden[3] {
		t.Fatal("token must use hidden input")
	}
	for i, secret := range hidden {
		if i != 3 && secret {
			t.Fatal("unexpected hidden prompt")
		}
	}
	if strings.Contains(out.String(), "private-token") || !strings.Contains(out.String(), "A value is required") || !strings.Contains(out.String(), "must be a JSON array") {
		t.Fatal("incorrect prompt output")
	}
}

func TestInteractiveConfigurationInterrupted(t *testing.T) {
	for stop := range 10 {
		count := 0
		values, err := promptConfiguration(io.Discard, func(string, bool) (string, error) {
			count++
			if count > stop {
				return "", io.EOF
			}
			if count <= 3 {
				return "value", nil
			}
			return "", nil
		})
		if err == nil || values != nil {
			t.Fatal("partial configuration accepted")
		}
	}
}

func TestTerminalLineDoesNotConsumeNextAnswer(t *testing.T) {
	in := strings.NewReader("workspace\r\ntoken\n")
	for _, want := range []string{"workspace", "token"} {
		value, err := readTerminalLine(in)
		if err != nil || value != want {
			t.Fatalf("line = %q, %v", value, err)
		}
	}
	if _, err := readTerminalLine(strings.NewReader("partial")); err == nil {
		t.Fatal("accepted interrupted line")
	}
}

func TestInteractiveAPIProxyValidation(t *testing.T) {
	for _, answer := range []string{"", "true", "false"} {
		lines := []string{"team", "email", "token", "", "", "", "", "", "invalid", answer, ""}
		var out bytes.Buffer
		values, err := promptConfiguration(&out, func(string, bool) (string, error) {
			if len(lines) == 0 {
				return "", io.EOF
			}
			value := lines[0]
			lines = lines[1:]
			return value, nil
		})
		if err != nil || values["BITBUCKET_ENABLE_API_PROXY"] != answer || !strings.Contains(out.String(), "must be a boolean") {
			t.Fatalf("API proxy prompt: %v, %s", err, out.String())
		}
	}
}
