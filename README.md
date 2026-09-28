# redline

A build tool for C++ on Linux/Unix, aiming for the `cargo init` experience
CMake/Meson/Makefiles never quite gave C++.

`redline` wraps CMake + Ninja rather than replacing them. You get a
transparent, hand-editable `CMakeLists.txt`, plus built-in support for gdb,
valgrind, and perf/flamegraph profiling without hand-assembling the
incantations yourself.

## Install

```sh
curl -sSL https://raw.githubusercontent.com/abhyuday-fr/Redline/main/install.sh | sh
```

Or download a release directly from the [Releases page](https://github.com/abhyuday-fr/Redline/releases).

### Requirements

- Linux or another Unix-like system
- `cmake` (>= 3.20), `ninja`, and `git` are required
- `gdb`, `valgrind`, `perf`, and a flamegraph renderer (`inferno` or
  Brendan Gregg's `FlameGraph` scripts) are optional. `redline doctor`
  checks for all of these and offers to install what's missing

## Quick start

```sh
redline rev myproject
cd myproject
redline run
```

## Commands

| Command | What it does |
|---|---|
| `redline rev [name]` | Initialize a new project (git init, `src/main.cpp`, manifest, `CMakeLists.txt`) |
| `redline build [--release]` | Compile |
| `redline run [--release] [--bin X]` | Build and run |
| `redline run --gdb` | Run under gdb; auto-backtrace on crash |
| `redline run --valgrind` | Run under valgrind memcheck (release profile only, see below) |
| `redline run --perf=fast\|detailed\|max [--flame]` | Profile with perf, optionally render a flamegraph |
| `redline test` | Build and run `tests/main.cpp` if present |
| `redline add <name>@<version> --git <url> [--dev] [--link-target X::Y]` | Add a FetchContent dependency |
| `redline todo [--markers=A,B] [dir]` | List TODO/FIXME/XXX/HACK/BUG/NOTE/OPTIMIZE/REVIEW comments with file and line |
| `redline clean` | Remove build artifacts |
| `redline doctor` | Check for required/optional tools, offer to install what's missing |

## Finding pending work

`redline todo` scans every `.c`, `.h`, `.cc`, `.hh`, `.cpp`, `.hpp`, `.cxx`,
and `.hxx` file under the current directory (skipping `build/`, `.git/`, and
`_deps/`) and lists marker comments with their file and line number:

```
src/bin/helper.cpp:1: XXX: XXX: this whole file needs a rewrite
src/main.cpp:3: TODO: TODO: add proper argument parsing
src/main.cpp:6: FIXME: FIXME: should return based on actual success

3 found across 2 file(s): 1 TODO, 1 FIXME, 1 XXX
```

Only real comments are scanned. A marker word inside a string literal
(`"http://example.com/?todo=1"`) or as part of an identifier
(`kTodoListCapacity`) is not reported. Matching is case-insensitive, and
`--markers=TODO,FIXME` narrows the search to specific markers. Pass a
directory to scan somewhere other than the current one.

Known gap: raw string literals (`R"(...)"`) aren't understood by the scanner.

## Project layout when using Redline

```
myproject/
├── redline.toml       # manifest: profiles, dependencies, binaries
├── CMakeLists.txt      # generated, but yours to edit outside the managed block
├── src/
│   ├── main.cpp        # default binary entry point
│   └── bin/            # extra binaries (declared in redline.toml)
├── tests/
│   └── main.cpp        # optional, auto-detected, enables `redline test`
└── build/
    ├── dev/             # separate build trees per profile
    └── release/
```

## The managed CMakeLists.txt block

`redline` generates and regenerates only the region between a line starting
with `# === ENGINE:BEGIN` and the line `# === ENGINE:END ===`.

Everything outside those markers is yours. `redline` never touches it, and
regeneration preserves it exactly. If the markers are missing or edited,
`redline` refuses to regenerate rather than guess. Pass `--force-regen` to
reset the managed block, or restore the markers by copying them from the
`CMakeLists.txt` of a freshly scaffolded project (they must match exactly).

## Known tool conflicts (handled automatically)

Two real conflicts came up during development, and `redline` accounts for
both:

- **AddressSanitizer/UBSan + valgrind**: these instrument memory access in
  incompatible ways. `redline run --valgrind` refuses to run against a
  profile with sanitizers enabled (the default `dev` profile) and tells you
  to use `--release` instead.
- **LeakSanitizer + gdb**: LeakSanitizer refuses to run under any
  ptrace-based debugger and treats that as a fatal error. `redline run
  --gdb` automatically sets `ASAN_OPTIONS=detect_leaks=0` so debugging
  still works, while keeping ASan's actual bug-detection and UBSan active.

## Dependency versions are required

`redline add` requires an explicit `@version`. An unpinned FetchContent
dependency isn't reproducible, and CMake's FetchContent falls back to
checking out a branch literally named `master` when no tag is given, which
fails outright on any repo whose default branch has a different name
(Catch2's is `devel`, for example).

## CMake target names aren't guessable

`redline` defaults to linking `<name>::<name>` for a dependency, which
works for libraries like `fmt` but not all of them. Catch2, for instance,
exports `Catch2::Catch2WithMain`. Use `--link-target` on `redline add` to
override it:

```sh
redline add catch2@v3.5.4 --git https://github.com/catchorg/Catch2.git \
    --dev --link-target Catch2::Catch2WithMain
```

## Status

v1. Binary projects only (no `--lib` support yet). No dependency registry:
`--git` is required on `redline add`. Linux/Unix only.

## License

MIT, see [LICENSE](LICENSE).
