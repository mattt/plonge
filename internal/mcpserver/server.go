// Package mcpserver serves plonge's operations to coding agents over MCP.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"runtime/debug"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattt/plonge/internal/disk"
	"github.com/mattt/plonge/internal/engine"
	"github.com/mattt/plonge/internal/global"
	"github.com/mattt/plonge/internal/mise"
	"github.com/mattt/plonge/internal/report"
)

const scanDescription = `Report the caches that developer tools and coding agents leave on disk, with their sizes, projects, and last activity. It deletes nothing. A project is a root directory recorded by mise. With global, it also lists caches outside projects and each rule's cleanup action. Show the sizes to the user before you suggest a cleanup.`

const cleanDescription = `Delete caches. This is a dry run unless apply is true. Always call it first without apply, show the dry run to the user, and get the user's approval before you call it with apply: true.

A quiet project has known activity at least quiet_hours old (default: 24 hours). Active projects have newer or unknown activity; include_active selects them too. min_size (such as "100MiB") leaves out smaller caches. A busy project has a running or initializing Paseo agent and is excluded. If Paseo is unavailable, its busy check provides no protection.

With global, it selects only global cleanup actions, without checking project activity. apply runs eligible actions. A prune rule uses the tool's cleanup policy; a clear rule can also remove cached data still in use. Clear rules require include_clear. Later commands may need to download dependencies or rebuild files. Show this effect before asking for approval. Sizes and freed space are estimates.`

const diskDescription = `Report the free space on the volume of the user's home directory. It only reads.`

type scanInput struct {
	Global bool `json:"global,omitempty" jsonschema:"also list caches outside projects"`
}

type cleanInput struct {
	Apply         bool   `json:"apply,omitempty" jsonschema:"delete; only after the user approves the dry run"`
	IncludeActive bool   `json:"include_active,omitempty" jsonschema:"also select active projects; exclude busy projects; requires global to be false"`
	MinSize       string `json:"min_size,omitempty" jsonschema:"skip project caches smaller than this size, such as 100MiB; requires global to be false"`
	Global        bool   `json:"global,omitempty" jsonschema:"work only on caches outside projects"`
	IncludeClear  bool   `json:"include_clear,omitempty" jsonschema:"with global, also select clear rules, which can remove cached data still in use"`
}

type diskInput struct{}

type diskOutput struct {
	FreeBytes int64  `json:"free_bytes"`
	Free      string `json:"free"`
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "devel"
}

// New returns a server with the scan, clean, and disk tools.
func New() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "plonge", Version: version()}, nil)
	destructive := true
	mcp.AddTool(s, &mcp.Tool{
		Name:        "scan",
		Description: scanDescription,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, handleScan)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "clean",
		Description: cleanDescription,
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &destructive},
	}, handleClean)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "disk",
		Description: diskDescription,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, handleDisk)
	return s
}

// Serve runs the server over standard input and output.
func Serve(ctx context.Context) error {
	return New().Run(ctx, &mcp.StdioTransport{})
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil, nil
}

func handleScan(ctx context.Context, _ *mcp.CallToolRequest, in scanInput) (*mcp.CallToolResult, any, error) {
	inv, err := engine.Inspect(ctx, engine.Selection{Global: in.Global})
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(inv.Report)
}

func handleClean(ctx context.Context, _ *mcp.CallToolRequest, in cleanInput) (*mcp.CallToolResult, any, error) {
	if in.Global {
		if in.IncludeActive || in.MinSize != "" {
			return nil, nil, errors.New("include_active and min_size do not apply with global")
		}
		res, _, err := engine.CleanGlobal(ctx, global.Options{Apply: in.Apply, IncludeClear: in.IncludeClear})
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(res)
	}
	if in.IncludeClear {
		return nil, nil, errors.New("include_clear needs global")
	}
	var floor int64
	if in.MinSize != "" {
		var err error
		if floor, err = disk.ParseSize(in.MinSize); err != nil {
			return nil, nil, err
		}
	}
	res, _, err := engine.Clean(ctx, engine.CleanOptions{
		Apply:         in.Apply,
		IncludeActive: in.IncludeActive,
		MinSize:       floor,
	})
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(res)
}

func handleDisk(_ context.Context, _ *mcp.CallToolRequest, _ diskInput) (*mcp.CallToolResult, any, error) {
	free, err := disk.Free(mise.EnvFromOS().Home)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(diskOutput{FreeBytes: free, Free: report.HumanBytes(free)})
}
