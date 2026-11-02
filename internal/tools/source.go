package tools

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edspc/bitbucket-cloud-mcp/internal/bitbucket"
)

const (
	maxSourceBytes   = 1 << 20
	sourceChunkBytes = 40 << 10
)

type sourceInput struct {
	Repo     string `json:"repo" jsonschema:"repository slug"`
	Revision string `json:"revision" jsonschema:"branch name, tag or commit hash; use the returned full commit SHA for continuations"`
	Path     string `json:"path" jsonschema:"repository-relative path with slash separators; empty for the root directory"`
}

func (in sourceInput) validate(directory bool, continuation bool) error {
	if in.Repo == "" || strings.TrimSpace(in.Revision) == "" || in.Revision == "." || in.Revision == ".." {
		return errors.New("repo and revision (branch, tag or commit hash) are required")
	}
	for _, part := range strings.Split(in.Revision, "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\\x00") {
			return errors.New("revision must be a branch, tag or commit without empty, dot or parent segments or backslashes")
		}
	}
	if in.Path != "" || !directory {
		for _, part := range strings.Split(in.Path, "/") {
			if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\\x00") {
				return errors.New("path must be repository-relative, without empty, dot or parent segments or backslashes")
			}
		}
	}
	if continuation && (len(in.Revision) != 40 || strings.Trim(in.Revision, "0123456789abcdefABCDEF") != "") {
		return errors.New("continuations require revision set to the full commit SHA returned by the first call")
	}
	return nil
}

type getFileInput struct {
	sourceInput
	Offset int `json:"offset,omitempty" jsonschema:"byte offset, 0 when omitted; pass next_offset and the returned commit as revision for more"`
}

type getFileResult struct {
	ContentTrust string `json:"content_trust"`
	Commit       string `json:"commit"`
	Path         string `json:"path"`
	Size         int    `json:"size"`
	Offset       int    `json:"offset"`
	Content      string `json:"content"`
	NextOffset   int    `json:"next_offset,omitempty"`
}

func (t *tools) getFile(ctx context.Context, _ *mcp.CallToolRequest, in getFileInput) (*mcp.CallToolResult, getFileResult, error) {
	if err := in.validate(false, in.Offset > 0); err != nil {
		return nil, getFileResult{}, err
	}
	if in.Offset < 0 || in.Offset > maxSourceBytes {
		return nil, getFileResult{}, errors.New("offset must be between 0 and 1048576")
	}
	commit, err := t.bb.ResolveRevision(ctx, in.Repo, in.Revision)
	if err != nil {
		return nil, getFileResult{}, err
	}
	data, err := t.bb.SourceFile(ctx, in.Repo, commit, in.Path, maxSourceBytes)
	if err != nil {
		return nil, getFileResult{}, err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, getFileResult{}, errors.New("file is binary or is not UTF-8 text")
	}
	if in.Offset > len(data) || in.Offset < len(data) && !utf8.RuneStart(data[in.Offset]) {
		return nil, getFileResult{}, errors.New("offset must be within the file and on a UTF-8 character boundary")
	}
	end := min(in.Offset+sourceChunkBytes, len(data))
	for end < len(data) && !utf8.RuneStart(data[end]) {
		end--
	}
	result := getFileResult{ContentTrust: "untrusted", Commit: commit, Path: in.Path, Size: len(data), Offset: in.Offset, Content: string(data[in.Offset:end])}
	if end < len(data) {
		result.NextOffset = end
	}
	return nil, result, nil
}

type listDirectoryInput struct {
	sourceInput
	Limit int `json:"limit,omitempty" jsonschema:"entries per page; 10 when omitted, at most 50"`
	Page  int `json:"page,omitempty" jsonschema:"page from 1; pass next_page with the returned commit as revision for more"`
}

type listDirectoryResult struct {
	ContentTrust string                  `json:"content_trust"`
	Commit       string                  `json:"commit"`
	Path         string                  `json:"path"`
	Entries      []bitbucket.SourceEntry `json:"entries"`
	NextPage     int                     `json:"next_page,omitempty"`
	Total        *int                    `json:"total,omitempty"`
}

func (t *tools) listDirectory(ctx context.Context, _ *mcp.CallToolRequest, in listDirectoryInput) (*mcp.CallToolResult, listDirectoryResult, error) {
	if err := in.validate(true, in.Page > 1); err != nil {
		return nil, listDirectoryResult{}, err
	}
	limit, err := optionalCount("limit", in.Limit, defaultListLimit, maxListLimit)
	if err != nil {
		return nil, listDirectoryResult{}, err
	}
	page, err := optionalPage(in.Page)
	if err != nil {
		return nil, listDirectoryResult{}, err
	}
	commit, err := t.bb.ResolveRevision(ctx, in.Repo, in.Revision)
	if err != nil {
		return nil, listDirectoryResult{}, err
	}
	entries, err := t.bb.ListDirectory(ctx, in.Repo, commit, in.Path, page, limit)
	if err != nil {
		return nil, listDirectoryResult{}, err
	}
	values := entries.Values
	if values == nil {
		values = []bitbucket.SourceEntry{}
	}
	return nil, listDirectoryResult{ContentTrust: "untrusted", Commit: commit, Path: in.Path, Entries: values, NextPage: nextPage(page, entries.More), Total: entries.Total}, nil
}
