//go:build !darwin || ios

package main

import (
	"errors"
	"io"
	"os"
)

func configurationLookup() (func(string) (string, bool), error) {
	return os.LookupEnv, nil
}

func configure(io.Reader, io.Writer) error {
	return errors.New("configure requires macOS Keychain")
}
