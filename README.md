# Plonge

Plonge finds the caches that developer tools and coding agents
leave on disk, with rules that you can extend.
It pairs with [mise](https://mise.jdx.dev/):
mise en place is the preparation, and the plonge is where the dishes get done.

This version only reports.
`plonge scan` reads your projects and prints what it finds;
it deletes nothing.

## Install

With mise, through its `go` backend:

```console
mise use -g go:github.com/mattt/plonge/cmd/plonge@latest
```

With Go 1.26 or later:

```console
go install github.com/mattt/plonge/cmd/plonge@latest
```

Plonge runs `git`, so Git must be on your `PATH`.

## Scan

```console
plonge scan
```

Plonge gets your projects from mise.
mise records each config file that it loads
in `$MISE_STATE_DIR/tracked-configs`
(by default, `~/.local/state/mise/tracked-configs`).
Plonge maps each file to its project root,
skips mise's global config and links whose target is gone,
and drops a root that is inside another root.

Use `--root` to add a directory; you can give it more than once.
Use `--no-mise` to scan only the `--root` directories.

```console
plonge scan --root ~/Code/scratch
plonge scan --no-mise --root ~/Code
```

In each root, plonge looks for directories with a cache name,
such as `node_modules` or `.venv`.
It does not follow symlinks or descend into a cache that it found.
A directory counts as a cache only when Git ignores it
and no tracked file is inside it.
A root that is not in a Git work tree is skipped.

The report lists each cache with its size on disk, its project,
the project's last Git activity, whether the project is quiet,
and the rule that matched.
A project is quiet when its Git HEAD log has not changed
for `quiet_hours` (by default, 24 hours).
The totals at the end give the size of all caches and of quiet ones.

Use `--json` to get the same data as JSON.

## Disk

`plonge disk` prints the free space on your home directory's volume.

```console
plonge disk
plonge disk --warn-below 20GiB
```

With `--warn-below`, plonge prints one warning line only when free space
is below that size, and it always exits 0, so that hooks can call it.
Sizes accept `KiB`, `MiB`, `GiB`, and `TiB` (and `K`, `M`, `G`, and `T`) as powers of 1024,
and `KB`, `MB`, `GB`, and `TB` as powers of 1000.

## Rules

Plonge has these default rules:

```toml
names = [".astro", ".build", ".gradle", ".mypy_cache", ".next", ".parcel-cache",
         ".pytest_cache", ".ruff_cache", ".svelte-kit", ".turbo", ".venv",
         "DerivedData", "__pycache__", "dist", "node_modules", "target", "venv"]
quiet_hours = 24

# Directories that the walk never enters.
skip = [".cache", ".git", ".hg", ".jj", ".svn"]

# A generic name counts only next to one of these project files.
[beside]
dist = ["package.json"]
target = ["Cargo.toml", "pom.xml"]
venv = ["pyproject.toml", "requirements.txt", "setup.py"]
```

To add rules, write them to `$XDG_CONFIG_HOME/plonge/rules.toml`
(by default, `~/.config/plonge/rules.toml`).
Your `names` and `skip` add to the defaults.
A `beside` entry in your file replaces the default entry for that name,
and a `quiet_hours` value replaces the default.
For example:

```toml
names = [".cache", "build"]
quiet_hours = 72
skip = ["third_party"]

[beside]
build = ["build.gradle", "build.gradle.kts"]
dist = ["package.json", "pyproject.toml"]
```

The walk never enters a directory in `skip`.
This keeps out caches that are nested in a tool's own cache,
such as `__pycache__` inside a `.cache/uv` environment.
A name in both `names` and `skip` is reported as a cache and is not entered.
`.git`, `.hg`, `.jj`, and `.svn` cannot be added to `names`.

## Development

The repository pins its tools in `mise.toml`.

```console
mise install
mise exec -- make check
```

`make build` writes `bin/plonge`.
`make lint` runs `gofmt -l` and `go vet`, and `make test` runs the tests with gotestsum.
