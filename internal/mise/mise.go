// Package mise reads the projects that mise has recorded on disk.
package mise

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Env holds the environment values that locate mise's files.
// Empty fields fall back to mise's defaults.
type Env struct {
	Home              string
	MiseStateDir      string
	XDGStateHome      string
	MiseConfigDir     string
	XDGConfigHome     string
	MiseGlobalConfig  string
	MiseSystemConfDir string
}

// EnvFromOS reads Env from the process environment.
func EnvFromOS() Env {
	home, _ := os.UserHomeDir()
	return Env{
		Home:              home,
		MiseStateDir:      os.Getenv("MISE_STATE_DIR"),
		XDGStateHome:      os.Getenv("XDG_STATE_HOME"),
		MiseConfigDir:     os.Getenv("MISE_CONFIG_DIR"),
		XDGConfigHome:     os.Getenv("XDG_CONFIG_HOME"),
		MiseGlobalConfig:  os.Getenv("MISE_GLOBAL_CONFIG_FILE"),
		MiseSystemConfDir: os.Getenv("MISE_SYSTEM_CONFIG_DIR"),
	}
}

// TrackedConfigsDir returns the directory where mise links each config file it loads.
func (e Env) TrackedConfigsDir() string {
	switch {
	case e.MiseStateDir != "":
		return filepath.Join(e.MiseStateDir, "tracked-configs")
	case e.XDGStateHome != "":
		return filepath.Join(e.XDGStateHome, "mise", "tracked-configs")
	default:
		return filepath.Join(e.Home, ".local", "state", "mise", "tracked-configs")
	}
}

func (e Env) configDir() string {
	switch {
	case e.MiseConfigDir != "":
		return e.MiseConfigDir
	case e.XDGConfigHome != "":
		return filepath.Join(e.XDGConfigHome, "mise")
	default:
		return filepath.Join(e.Home, ".config", "mise")
	}
}

// isGlobal reports whether a config file is one of mise's global or system configs.
func (e Env) isGlobal(file string) bool {
	if e.MiseGlobalConfig != "" && filepath.Clean(e.MiseGlobalConfig) == file {
		return true
	}
	sys := e.MiseSystemConfDir
	if sys == "" {
		sys = "/etc/mise"
	}
	for _, dir := range []string{e.configDir(), sys} {
		if within(file, filepath.Clean(dir)) {
			return true
		}
	}
	// A config file whose project root is the home directory,
	// such as ~/.tool-versions or ~/.config/mise.toml, applies everywhere.
	return e.Home != "" && ProjectRoot(file) == filepath.Clean(e.Home)
}

// Projects is the result of reading mise's tracked configs.
type Projects struct {
	Roots       []string // project roots, sorted, with nested roots removed
	BrokenLinks int      // links whose target is missing or is not a regular file
}

// ReadProjects reads the tracked-configs directory and maps each link to a project root.
// A missing directory gives no roots and no error.
func ReadProjects(e Env) (Projects, error) {
	dir := e.TrackedConfigsDir()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return Projects{}, nil
	}
	if err != nil {
		return Projects{}, err
	}
	var p Projects
	var roots []string
	for _, ent := range entries {
		link := filepath.Join(dir, ent.Name())
		target, err := os.Readlink(link)
		if err != nil {
			continue // not a symlink
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(dir, target)
		}
		target = filepath.Clean(target)
		info, err := os.Stat(target)
		if err != nil || !info.Mode().IsRegular() {
			p.BrokenLinks++
			continue
		}
		if e.isGlobal(target) {
			continue
		}
		roots = append(roots, ProjectRoot(target))
	}
	p.Roots = DropNested(roots)
	return p, nil
}

// ProjectRoot maps a mise config file to the root of its project.
// Files in .config, .config/mise, .config/mise/conf.d, mise, or .mise
// belong to the directory above those directories.
func ProjectRoot(file string) string {
	dir := filepath.Dir(file)
	base := filepath.Base(file)
	if filepath.Base(dir) == "conf.d" && filepath.Base(filepath.Dir(dir)) == "mise" {
		dir = filepath.Dir(dir)
		base = "config.toml"
	}
	switch filepath.Base(dir) {
	case "mise", ".mise":
		// mise/config.toml, .mise/config.toml, and .config/mise/config.toml.
		// A project named "mise" with mise.toml at its top stays as it is.
		if strings.HasPrefix(base, "config") {
			dir = filepath.Dir(dir)
			if filepath.Base(dir) == ".config" {
				dir = filepath.Dir(dir)
			}
		}
	case ".config":
		// .config/mise.toml and .config/mise.local.toml.
		if strings.HasPrefix(base, "mise") {
			dir = filepath.Dir(dir)
		}
	}
	return dir
}

// DropNested sorts and removes duplicate roots and roots inside another root.
func DropNested(roots []string) []string {
	clean := make([]string, 0, len(roots))
	for _, r := range roots {
		clean = append(clean, filepath.Clean(r))
	}
	sort.Strings(clean)
	var out []string
	for _, r := range clean {
		nested := false
		for _, o := range out {
			if within(r, o) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, r)
		}
	}
	return out
}

// within reports whether path is dir or inside dir.
func within(path, dir string) bool {
	if path == dir {
		return true
	}
	if dir == string(filepath.Separator) {
		return strings.HasPrefix(path, dir)
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}
