# polkadot

[![ci](https://github.com/taskie/polkadot/actions/workflows/ci.yml/badge.svg)](https://github.com/taskie/polkadot/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/taskie/polkadot/branch/main/graph/badge.svg)](https://codecov.io/gh/taskie/polkadot)

An application to generate dotfiles from https://github.com/taskie/dotfiles .

## Usage

```
polkadot [-c <polkadot.yml>] [-n] [-raw] [-v] [-V] [<component-dir> ...]
```

- `-c` — path to a `polkadot.yml` config (default: `./polkadot.yml` if present).
- `-n` — dry run; resolve everything but don't write any files.
- `-raw` — concatenate fragments without normalizing newlines.
- `-v` — verbose; show status headers, debug logs, and each target's sources.
- `-V` — print the version and exit.

The positional arguments are *component directories* that hold the fragments
and config to assemble. If given, they replace `components` in `polkadot.yml`.

Without a `polkadot.yml`, `polkadot` runs from your dotfiles root (the current
working directory), which must contain `entry.yml`.

### Example

Lay out a dotfiles repo like this:

```
dotfiles/
├── entry.yml            # tags that describe this machine
└── common/              # a component directory
    ├── tags.yml         # tag dependency graph
    ├── rules.yml        # which fragments build which output files
    ├── paths.yml        # resolve tag values from the host
    └── bash/
        ├── 00-base.sh         # always included
        ├── 10-linux_linux.sh  # included only when the `linux` tag is set
        └── 20-prompt_gtp.sh   # rendered as a Go template (`gtp` tag)
```

`entry.yml` activates the tags for the current host (an empty value defaults to
the key name):

```yaml
linux:
arch:
```

`common/rules.yml` maps an output file to the fragments that compose it:

```yaml
~/.bashrc:
  dir: /bash
  pat: \.sh$
  mode: "644"
```

`common/paths.yml` resolves tag values by probing the system:

```yaml
emacs:
  - type: exec   # value becomes the absolute path found via `which`
```

Generate the dotfiles:

```sh
cd path/to/dotfiles
polkadot -n common   # preview what would be written
polkadot common      # write the files (here, ~/.bashrc)
```

### `polkadot.yml`

Instead of passing everything on the command line, put a `polkadot.yml` in the
dotfiles root. Relative paths in it are resolved against its own directory, so
it works from any cwd:

```yaml
entries:            # merged in order, later wins (default: [entry.yml])
  - entry.yml
  - hosts/myhost.yml
  - path: entry.local.yml   # skipped if it does not exist
    optional: true
tags:               # inline entry tags, merged last
  wsl:
components:         # component dirs, later ones override earlier ones
  - common
  - path: private   # skipped if it does not exist
    optional: true
raw: false          # same as -raw
```

```sh
polkadot -c ~/dotfiles/polkadot.yml -n
```

For each output file, matching fragments are concatenated in sorted order; a
fragment is included only when every tag encoded in its filename
(`name_tag1_tag2.ext`) is active. Fragments tagged `gtp` are rendered with Go's
`text/template`, receiving the resolved tag map as their data.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full pipeline.

## License

Apache 2.0
