package mcpserver

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mattt/plonge/internal/clean"
	"github.com/mattt/plonge/internal/global"
)

// A dry run of the clean tool records what it selected under a preview token,
// and apply deletes only when the same arguments select the same caches.
// The server keeps the previews in memory, so a restart invalidates them.

// previewTTL is how long a preview token stays valid.
// The user reads the dry run and approves it in the same conversation;
// after this time, the dry run that the user saw is probably out of date.
const previewTTL = 30 * time.Minute

// maxPreviews limits the previews that the server keeps.
// Adding one more drops the oldest.
const maxPreviews = 16

// cleanArgs are the arguments of the clean tool that choose the selection.
type cleanArgs struct {
	IncludeActive bool
	MinSize       string
	Global        bool
	IncludeClear  bool
}

func (a cleanArgs) String() string {
	return fmt.Sprintf("include_active=%t, min_size=%q, global=%t, include_clear=%t",
		a.IncludeActive, a.MinSize, a.Global, a.IncludeClear)
}

// selection maps each selected cache path or global rule ID
// to a description of its identity.
// Two selections are equal only when the keys and the descriptions match.
type selection map[string]string

// projectSelection describes each cache that a project dry run would delete
// by its rule, project, size, and modification time.
func projectSelection(res clean.Result) selection {
	sel := selection{}
	for _, it := range res.Items {
		sel[it.Path] = fmt.Sprintf("rule %s in %s, %d bytes, modified %s",
			it.Rule, displayPath(it.Project), it.Bytes, modTime(it.Path))
	}
	return sel
}

// globalSelection describes each rule that a global dry run would run
// by the inputs of its action, as global.Inputs encodes them.
// It leaves out the size of a shared cache, which every build can change,
// but keeps the size of the folders that the Xcode rule deletes.
func globalSelection(res global.Result) selection {
	sel := selection{}
	for _, o := range res.Outcomes {
		if o.Status == global.WouldRun {
			sel[o.ID] = o.Inputs().String()
		}
	}
	return sel
}

func modTime(path string) string {
	info, err := os.Lstat(path)
	if err != nil {
		return "unknown"
	}
	return info.ModTime().UTC().Format(time.RFC3339Nano)
}

// diff lists how now differs from s, sorted.
func (s selection) diff(now selection) []string {
	var out []string
	for key, was := range s {
		is, ok := now[key]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("no longer selected: %s (%s)", displayPath(key), was))
		case is != was:
			out = append(out, fmt.Sprintf("changed: %s (was %s; now %s)", displayPath(key), was, is))
		}
	}
	for key, is := range now {
		if _, ok := s[key]; !ok {
			out = append(out, fmt.Sprintf("newly selected: %s (%s)", displayPath(key), is))
		}
	}
	slices.Sort(out)
	return out
}

// check returns an error that lists the differences when now is not s.
func (s selection) check(now selection) error {
	d := s.diff(now)
	if len(d) == 0 {
		return nil
	}
	return fmt.Errorf("the selection changed after the dry run, so nothing was deleted; "+
		"call clean without apply again and show the new dry run to the user:\n- %s",
		strings.Join(d, "\n- "))
}

type preview struct {
	args    cleanArgs
	sel     selection
	expires time.Time
}

// previews holds the recent dry runs by token.
type previews struct {
	mu     sync.Mutex
	now    func() time.Time
	tokens map[string]preview
	order  []string // oldest first
}

func newPreviews(now func() time.Time) *previews {
	return &previews{now: now, tokens: map[string]preview{}}
}

// add records a dry run and returns its token and expiry time.
func (p *previews) add(args cleanArgs, sel selection) (string, time.Time) {
	var b [16]byte
	_, _ = rand.Read(b[:])
	token := base64.RawURLEncoding.EncodeToString(b[:])
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	p.order = slices.DeleteFunc(p.order, func(t string) bool {
		if now.After(p.tokens[t].expires) {
			delete(p.tokens, t)
			return true
		}
		return false
	})
	for len(p.order) >= maxPreviews {
		delete(p.tokens, p.order[0])
		p.order = p.order[1:]
	}
	expires := now.Add(previewTTL)
	p.tokens[token] = preview{args: args, sel: sel, expires: expires}
	p.order = append(p.order, token)
	return token, expires
}

var errNoToken = errors.New("apply requires the preview_token from a dry run with the same arguments; " +
	"call clean without apply, show the dry run to the user, and get approval first")

// take removes the preview for token and returns it.
// A token works for one apply call only, even when that call fails.
func (p *previews) take(token string) (preview, error) {
	if token == "" {
		return preview{}, errNoToken
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	pv, ok := p.tokens[token]
	if !ok {
		return preview{}, errors.New("unknown preview_token; it was already used, replaced by newer dry runs, " +
			"or issued before the server restarted; call clean without apply again")
	}
	delete(p.tokens, token)
	p.order = slices.DeleteFunc(p.order, func(t string) bool { return t == token })
	if p.now().After(pv.expires) {
		return preview{}, fmt.Errorf("the preview_token expired %s after its dry run; call clean without apply again", previewTTL)
	}
	return pv, nil
}
