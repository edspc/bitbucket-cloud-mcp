package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
	"github.com/edspc/bitbucket-cloud-mcp/internal/tools"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if len(os.Args) == 2 && os.Args[1] == "configure" {
		if err := configure(os.Stdin, os.Stderr); err != nil {
			log.Error("configuration failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if handled, err := commandLine(os.Args[1:], os.Stdout); handled {
		if err != nil {
			log.Error("invalid command line", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(context.Background(), log); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	lookup, err := configurationLookup()
	if err != nil {
		return err
	}
	cfg, err := configFromLookup(lookup)
	if err != nil {
		return err
	}
	bb := bitbucket.New(&http.Client{Timeout: 30 * time.Second}, cfg)
	policy, err := policyFromLookup(lookup)
	if err != nil {
		return err
	}
	s := tools.NewServer(ctx, &mcp.Implementation{Name: "bitbucket-cloud-mcp", Version: version}, bb, log, policy)
	log.Info("serving MCP over stdio", "workspace", cfg.Workspace)
	return s.Run(ctx, &mcp.StdioTransport{})
}

func configFromLookup(lookup func(string) (string, bool)) (bitbucket.Config, error) {
	var missing []string
	get := func(name string) string {
		v, _ := lookup(name)
		if v == "" {
			missing = append(missing, name)
		}
		return v
	}
	cfg := bitbucket.Config{
		Workspace: get("BITBUCKET_WORKSPACE"),
		Email:     get("BITBUCKET_EMAIL"),
		Token:     get("BITBUCKET_API_TOKEN"),
	}
	if len(missing) > 0 {
		return cfg, fmt.Errorf("missing configuration parameters: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}
