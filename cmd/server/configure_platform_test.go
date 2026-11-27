package main

import (
	"go/build"
	"slices"
	"testing"
)

func TestConfigurationUsesPlatformFiles(t *testing.T) {
	for _, platform := range []string{"darwin", "ios", "linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS, ctx.GOARCH = platform, "arm64"
			pkg, err := ctx.ImportDir(".", 0)
			if err != nil {
				t.Fatal(err)
			}
			useKeychain := platform == "darwin"
			for _, file := range []string{"keychain_darwin.go", "configure_darwin.go"} {
				if slices.Contains(pkg.GoFiles, file) != useKeychain {
					t.Errorf("unexpected %s selection for %s", file, platform)
				}
			}
			if slices.Contains(pkg.GoFiles, "keychain_other.go") == useKeychain {
				t.Errorf("unexpected keychain_other.go selection for %s", platform)
			}
		})
	}
}
