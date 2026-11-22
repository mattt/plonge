// Package mcpserver serves plonge's operations to coding agents over MCP.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattt/plonge/internal/clean"
	"github.com/mattt/plonge/internal/disk"
	"github.com/mattt/plonge/internal/engine"
	"github.com/mattt/plonge/internal/global"
	"github.com/mattt/plonge/internal/mise"
	"github.com/mattt/plonge/internal/report"
)

const scanDescription = `Report the caches that developer tools and coding agents leave on disk, with their sizes, projects, and last activity. It deletes nothing. A project is a root directory recorded by mise. With global, it also lists caches outside projects and each rule's cleanup action. Show the sizes to the user before you suggest a cleanup.`

const cleanDescription = `Delete caches. This is a dry run unless apply is true. Always call it first without apply, show the dry run to the user, and get the user's approval before you call it with apply: true.

A dry run returns preview_token. apply requires that token and the same include_active, min_size, global, and include_clear as the dry run. It scans again and deletes nothing unless the new selection is the one in the dry run: the same cache paths with the same sizes and modification times, or the same global rules with the same paths, actions, listed targets and their installation paths, keep_names, and Xcode folders with their total size. If the selection changed, the error lists the differences; show them to the user and run a new dry run. A token works for one apply call and expires 30 minutes after its dry run. The server cannot tell whether the user approved; only call apply after the user approves the dry run that returned the token.

A quiet project has known activity at least quiet_hours old (default: 24 hours). Active projects have newer or unknown activity; include_active selects them too. min_size (such as "100MiB") leaves out smaller caches. A busy project has a running or initializing Paseo agent and is excluded. If paseo is on PATH but its status cannot be read, the paseo entry in activity_sources has status "failed" and every project is busy, so nothing is deleted; tell the user the reason. If paseo is not on PATH, the busy check provides no protection.

With global, it selects only global cleanup actions, without checking project activity. apply runs eligible actions. A prune rule uses the tool's cleanup policy; a clear rule can also remove cached data still in use. Clear rules require include_clear. Later commands may need to download dependencies or rebuild files. Show this effect before asking for approval. In a global dry run, folders lists the folders that the Xcode rule deletes, and target_paths lists the installation paths that a rule's targets removes; show these paths to the user. A path that is not valid UTF-8, or that starts with a double quote, appears in Go's quoted form, in double quotes with \x escapes for bytes that are not UTF-8. Each list shows at most 500 paths, and paths_omitted counts the rest, which apply also removes; tell the user how many paths were not shown. Sizes and freed space are estimates. When a rule has whole_cache: true, bytes is the size of a prune rule's whole cache, and the tool's policy removes only part of it; do not present it as the space to free. With apply, home_volume has the free space on the home directory's volume before and after the cleanup; other processes can change it too. Report gained_bytes next to freed_bytes.`

const diskDescription = `Report the free space on the volume of the user's home directory. It only reads.`

type scanInput struct {
	Global bool `json:"global,omitempty" jsonschema:"also list caches outside projects"`
}

type cleanInput struct {
	Apply         bool   `json:"apply,omitempty" jsonschema:"delete; only after the user approves the dry run; requires preview_token"`
	PreviewToken  string `json:"preview_token,omitempty" jsonschema:"with apply, the preview_token of the approved dry run"`
	IncludeActive bool   `json:"include_active,omitempty" jsonschema:"also select active projects; exclude busy projects; requires global to be false"`
	MinSize       string `json:"min_size,omitempty" jsonschema:"skip project caches smaller than this size, such as 100MiB; requires global to be false"`
	Global        bool   `json:"global,omitempty" jsonschema:"work only on caches outside projects"`
	IncludeClear  bool   `json:"include_clear,omitempty" jsonschema:"with global, also select clear rules, which can remove cached data still in use"`
}

func (in cleanInput) args() cleanArgs {
	return cleanArgs{IncludeActive: in.IncludeActive, MinSize: in.MinSize, Global: in.Global, IncludeClear: in.IncludeClear}
}

// cleanPreview is a project dry run with its token.
type cleanPreview struct {
	clean.Result
	PreviewToken   string    `json:"preview_token"`
	PreviewExpires time.Time `json:"preview_expires"`
}

// globalPreview is a global dry run with its token.
// Its outcomes also list the paths that each rule selected.
type globalPreview struct {
	global.Result
	Outcomes       []previewOutcome `json:"outcomes"`
	PreviewToken   string           `json:"preview_token"`
	PreviewExpires time.Time        `json:"preview_expires"`
}

// maxPreviewPaths limits each list of paths in a global dry run,
// so that a very large selection does not make the result too long to read.
// The preview token still covers every path, including those not shown.
const maxPreviewPaths = 500

// previewOutcome is a global outcome with the paths that it selected,
// which the outcome keeps out of its own JSON.
type previewOutcome struct {
	global.Outcome
	// Folders are the folders that the Xcode rule deletes.
	Folders []string `json:"folders,omitempty"`
	// TargetPaths are the installation paths that a rule's targets lists.
	TargetPaths []string `json:"target_paths,omitempty"`
	// PathsOmitted counts the paths left out of Folders and TargetPaths.
	PathsOmitted int `json:"paths_omitted,omitempty"`
}

func previewOutcomes(res global.Result) []previewOutcome {
	out := make([]previewOutcome, len(res.Outcomes))
	for i, o := range res.Outcomes {
		folders, n := firstPaths(o.Folders())
		targets, m := firstPaths(o.TargetPaths())
		out[i] = previewOutcome{Outcome: o, Folders: folders, TargetPaths: targets, PathsOmitted: n + m}
	}
	return out
}

// firstPaths returns at most maxPreviewPaths of paths, in order,
// as displayPath shows them, and how many it left out.
func firstPaths(paths []string) ([]string, int) {
	omitted := 0
	if len(paths) > maxPreviewPaths {
		paths, omitted = paths[:maxPreviewPaths], len(paths)-maxPreviewPaths
	}
	shown := make([]string, len(paths))
	for i, p := range paths {
		shown[i] = displayPath(p)
	}
	return shown, omitted
}

// displayPath returns path as JSON can carry it without loss.
// JSON replaces each byte that is not valid UTF-8 with U+FFFD,
// so two different names could look the same.
// A path that is not valid UTF-8, or that starts with a double quote,
// is shown in Go's quoted form, as strconv.Quote writes it,
// with \x escapes for those bytes; every other path is shown as it is.
// A shown path that starts with a double quote is therefore always quoted.
func displayPath(path string) string {
	if !utf8.ValidString(path) || strings.HasPrefix(path, `"`) {
		return strconv.Quote(path)
	}
	return path
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
	return newServer(time.Now)
}

// newServer returns a server whose preview tokens use the clock now.
func newServer(now func() time.Time) *mcp.Server {
	c := &cleaner{previews: newPreviews(now)}
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
	}, c.handleClean)
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

// cleaner serves the clean tool and keeps its preview tokens.
type cleaner struct {
	previews *previews
}

func (c *cleaner) handleClean(ctx context.Context, _ *mcp.CallToolRequest, in cleanInput) (*mcp.CallToolResult, any, error) {
	var floor int64
	if in.Global {
		if in.IncludeActive || in.MinSize != "" {
			return nil, nil, errors.New("include_active and min_size do not apply with global")
		}
	} else {
		if in.IncludeClear {
			return nil, nil, errors.New("include_clear needs global")
		}
		if in.MinSize != "" {
			var err error
			if floor, err = disk.ParseSize(in.MinSize); err != nil {
				return nil, nil, err
			}
		}
	}
	args := in.args()
	var approved selection
	if in.Apply {
		p, err := c.previews.take(in.PreviewToken)
		if err != nil {
			return nil, nil, err
		}
		if p.args != args {
			return nil, nil, fmt.Errorf("the preview_token is for a dry run with other arguments (%s), but apply has %s; "+
				"call clean without apply with these arguments again", p.args, args)
		}
		approved = p.sel
	} else if in.PreviewToken != "" {
		return nil, nil, errors.New("preview_token needs apply")
	}
	if in.Global {
		res, _, err := engine.CleanGlobal(ctx, engine.GlobalOptions{
			Options: global.Options{Apply: in.Apply, IncludeClear: in.IncludeClear},
			Confirm: func(dry global.Result) error { return approved.check(globalSelection(dry)) },
		})
		if err != nil {
			return nil, nil, err
		}
		if in.Apply {
			return jsonResult(res)
		}
		token, expires := c.previews.add(args, globalSelection(res))
		return jsonResult(globalPreview{Result: res, Outcomes: previewOutcomes(res), PreviewToken: token, PreviewExpires: expires})
	}
	res, _, err := engine.Clean(ctx, engine.CleanOptions{
		Apply:         in.Apply,
		IncludeActive: in.IncludeActive,
		MinSize:       floor,
		Confirm:       func(dry clean.Result) error { return approved.check(projectSelection(dry)) },
	})
	if err != nil {
		return nil, nil, err
	}
	if in.Apply {
		return jsonResult(res)
	}
	token, expires := c.previews.add(args, projectSelection(res))
	return jsonResult(cleanPreview{Result: res, PreviewToken: token, PreviewExpires: expires})
}

func handleDisk(_ context.Context, _ *mcp.CallToolRequest, _ diskInput) (*mcp.CallToolResult, any, error) {
	free, err := disk.Free(mise.EnvFromOS().Home)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(diskOutput{FreeBytes: free, Free: report.HumanBytes(free)})
}
