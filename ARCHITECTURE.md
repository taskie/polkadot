# Architecture

`polkadot` is a small, single-binary command-line tool that **generates dotfiles**
by assembling fragments scattered across one or more component directories. It is
the engine behind [taskie/dotfiles](https://github.com/taskie/dotfiles): given a
set of *tags* describing the current machine (OS, distro, installed programs,
preferences), it selects the relevant fragments, optionally renders them as
templates, concatenates them, and writes the resulting dotfiles into your home
directory.

The whole program lives in one file, `polkadot.go` (~800 lines). There are no
internal packages; the design is a linear pipeline expressed as methods on a
single `App` struct.

## At a glance

- **Language / module:** Go (`module github.com/taskie/polkadot`, Go 1.21).
- **Dependencies:** `go.yaml.in/yaml/v3` (config parsing), `github.com/fatih/color`
  (colored progress output). Everything else is the standard library.
- **Entry point:** `main()` → `run()` in `polkadot.go`.
- **Release:** GoReleaser (`.goreleaser.yml`) builds static (`CGO_ENABLED=0`)
  binaries for linux/windows/darwin, injecting the tag into `main.version` via
  `-ldflags -X`. Without it, `-V` falls back to the module version from
  `runtime/debug.ReadBuildInfo` (e.g. `go install ...@v0.2.0`), else `dev`.
- **Tests:** `polkadot_test.go` covers the tag `Expander` (the trickiest part).

## Invocation

```
polkadot [-c <polkadot.yml>] [-n] [-raw] [-v] [-V] [<component-dir> ...]
```

- `-c` — path to a `polkadot.yml` config. Defaults to `./polkadot.yml` if it
  exists.
- `-n` — dry run: do everything except write output files.
- `-raw` — concatenate fragments verbatim (no newline normalization).
- `-v` — verbose: status headers and debug logs (stderr), and each target's
  source fragments (stdout).
- `-V` — print version and exit.
- positional args — the *component directories* (`polkaDirPaths`) to scan.
  When given, they **replace** `components` from `polkadot.yml`.

`NewApp` resolves these inputs (CLI > `polkadot.yml` > defaults):

- **With `polkadot.yml`:** the directory containing it is the dotfiles root
  (`dotfilesDirPath`), and every relative path in it (`entries`, `components`)
  is resolved against that directory, independent of the cwd.
- **Without it (legacy):** the current working directory is the dotfiles root
  and must contain `entry.yml`; components come from the positional args.

Component directories are scanned in the order given; later directories
override earlier ones for same-keyed config.

### `polkadot.yml`

```yaml
entries:        # entry files merged in order (later wins); default [entry.yml],
  - entry.yml   # `entries: []` disables it
  - hosts/myhost.yml
  - path: entry.local.yml   # object form; an optional file is skipped
    optional: true          # when it does not exist
tags:           # inline entry tags, merged after the entry files
  wsl:
components:     # component dirs, in override order (same forms as entries)
  - common
  - linux
  - path: private
    optional: true
raw: false      # same as -raw
```

Unknown keys are rejected (`Decoder.KnownFields(true)`, plus a manual key check
in `PathSpec.UnmarshalYAML`, which `KnownFields` does not reach) to catch typos.

All YAML files are decoded through `decodeYAML`, which rejects unknown struct
fields and duplicate keys. `rules.yml` additionally requires `dir`/`dirs` and
`pat` per rule; `paths.yml` requires a known `type` and a `path` for
`file`/`dir` candidates (`PathsConf.Validate`).

## Core data model

Everything funnels into one **tag map** (`map[string]string`): tag name → value
(usually a resolved path or an identifier). A fragment is included only if *all*
the tags encoded in its filename are present in this map. Templated fragments
additionally receive the tag map as their template context.

The central in-memory state is the `App` struct, whose fields mirror the pipeline
stages:

| Field | Stage | Meaning |
|-------|-------|---------|
| `dotfilesDirPath`, `entryPath`, `polkaDirPaths` | Input | CLI-derived inputs |
| `entryTags` | Load | tags declared in the entry files and inline `tags` |
| `tagConf` | Load | tag → implied-child-tags graph (`tags.yml`) |
| `ruleConfMap` | Load | output file → weave rule (`rules.yml`) |
| `tagMap` | Collect | the final resolved tag map |
| `dotEntries` | Weave | output files paired with their source fragments |

## Pipeline

`run()` builds an `App` and calls `Prepare()` then (unless `-n`) `Execute()`.
`Prepare()` orchestrates five stages; `Execute()` runs the sixth.

```
entry.yml ─┐
tags.yml ──┤  LoadEntry / LoadTags / LoadRules
rules.yml ─┘            │
                        ▼
            Expand  (resolve the tag graph, honor negation)
                        │
                        ▼
paths.yml ──►  Collect (probe the system: exec/file/dir/env)
                        │
                        ▼  → tagMap
              Weave  (scan component dirs, match files to rules,
                      filter by filename tags) → dotEntries
                        │
                        ▼
             Generate (concat fragments, render templates, write files)
```

### 1. Load (`LoadEntry`, `LoadTags`, `LoadRules`)

`CheckComponents` first verifies that every component path exists and is a
directory; a missing or non-directory component is an error, except that a
missing `optional` component is skipped. The surviving paths become
`polkaDirPaths`, which every later stage iterates.

- **entry files** (`entry.yml` in the dotfiles root, or the `entries` list of
  `polkadot.yml`) — each a flat `map[string]string` of the tags this machine
  should activate. A missing file is an error unless marked `optional`, in
  which case it is skipped. Files are merged in order, then inline `tags` from
  `polkadot.yml` on top. An empty value defaults to the key itself. A
  built-in `default: default` tag is always added.
- **`<dir>/tags.yml`** — a `map[tag]map[childTag]value`: declaring a tag pulls in
  its child tags. This forms the dependency graph expanded in stage 2. Loaded
  from every component dir; later dirs overwrite earlier definitions per tag.
- **`<dir>/rules.yml`** — `map[outputFile]WeaverEntry`. Each rule says which
  source subdirectories to scan (`dir` / `dirs`), a regexp `pat` selecting files,
  and an optional octal `mode` for the generated file. Parsed into `WeaverRule`
  (with a compiled `*regexp.Regexp` and a `*int` mode validated to `0..0777`).

### 2. Expand (`Expander`)

Turns the declared `entryTags` into a closed set of accepted/rejected tags by a
**breadth-first walk** over the `tags.yml` graph. Key rules:

- A tag prefixed with `!` is **negated** (rejected). Multiple `!`s encode both
  *parity* (odd = negative) and *importance* (the count) — so `!!tag` is a
  double-negative that re-accepts, and higher `!` counts win conflicts.
- Results are sorted by importance desc, then BFS depth, then name/value, then
  de-duplicated so the nearest/strongest declaration of each tag wins.
- Output: `acceptedTags` and `rejectedTags` maps.

This is the most subtle logic in the codebase and is the focus of
`polkadot_test.go` (regular, negation, and double-negative cases).

### 3. Collect (`Collector`, `paths.yml`)

Probes the host system to resolve dynamic tag values. **`<dir>/paths.yml`** maps
a tag key to a list of candidate `CollectorEntry` items; the first that resolves
wins. Entry `type`s:

- `exec` — `exec.LookPath(name)`; value = absolute path to the executable.
- `file` / `dir` — `os.Stat` a path (with `~/` expansion) and check it is the
  right kind; value = absolute path.
- `env` — value = `os.Getenv(name)`.

The collected map is then merged into `tagMap` along with the built-in
`dotfiles` (the root path) and `gtp` tags, the `acceptedTags` are layered on top,
and `rejectedTags` are deleted. The result is the authoritative `tagMap`.

### 4. Weave (`Weaver`)

For each rule, walks `<rootDir><ruleDir>` across every component dir and every
configured subdirectory, keeping files whose name matches the rule's regexp.

- **Filename tagging:** a fragment's basename (extension stripped recursively) is
  split on `_`; everything after the first segment is treated as required tags
  (`extractTagsFromPath`). A fragment is kept only if **all** its tags are in
  `tagMap`. This is how machine-specific fragments are switched on/off.
- Matching sources are grouped per output file, sorted by name, and
  de-duplicated by path (`mergeSourceArrayMap` / `removeDuplicatedDotSource`).
- Output is a sorted `[]DotEntry`, each pairing a `DotTarget` (output path +
  mode) with its ordered `[]DotSource`. Sorting makes the build deterministic.
- `Prepare` drops entries with no sources (every fragment gated off, or the
  rule's directories missing) and reports each as `info: <path>: no sources,
  skipped` on stderr, so an existing file is never replaced with an empty one.

### 5. Generate (`Generator`)

For each `DotEntry`: expand `~/` in the target path, `mkdir -p` the parent
directory, **concatenate all source fragments** in memory, and write the result
atomically (`writeFileAtomic`): a temporary file in the same directory gets the
content and the rule's mode (default `0644`, applied exactly, also to existing
files), then is renamed over the target. A failed write never leaves a partial
dotfile. If the target is a symlink, the file it points to is replaced and the
link is kept.

- Fragments tagged `gtp` (the built-in "go-template" tag) are rendered through
  Go's `text/template` with `tagMap` as the data context.
- All other fragments are copied verbatim.

## Conventions a component directory must follow

A component (polka) directory may contain any of these config files (all
optional, all merged across multiple directories):

- `tags.yml` — tag dependency graph.
- `rules.yml` — output file ⇒ which source dirs/patterns/mode.
- `paths.yml` — how to resolve tag values from the host.
- source fragment files under the directories named by `rules.yml`, named
  `something_tag1_tag2.ext` to gate them on tags.

The dotfiles root (the directory of `polkadot.yml`, or the cwd in legacy mode)
supplies `entry.yml` (or the files listed in `entries`), the per-machine tag
declaration.

## Notable design choices

- **Single file, no abstractions over the filesystem.** Each stage is a small
  struct (`Collector`, `Expander`, `Weaver`, `Generator`) with one public method;
  `App` wires them together. Easy to read top-to-bottom.
- **Determinism by sorting** at the merge and entry-assembly steps, so repeated
  runs produce identical output.
- **Layered overrides** everywhere: multiple component dirs are processed in
  order and later ones win, enabling a base + per-host layering scheme.
- **Fail fast, quiet by default.** Errors bubble up to `main()`, which prints a
  red "Failed" with the error to stderr and exits non-zero. stdout carries only
  the result (target files, plus their sources with `-v`); colored status
  headers and the resolved tag maps go to stderr and only with `-v`. `info:`
  messages (e.g. skipped rules) go to stderr regardless of `-v`.
