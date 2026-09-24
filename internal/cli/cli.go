// Package cli implements the redline command-line surface: rev, build,
// run, test, add, clean, doctor. Uses stdlib flag rather than a third-party
// framework (see project notes on the sandbox's restricted module proxy —
// this can be swapped for Cobra later without changing package boundaries).
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abhyuday-fr/Redline/internal/cmake"
	"github.com/abhyuday-fr/Redline/internal/doctor"
	"github.com/abhyuday-fr/Redline/internal/manifest"
	"github.com/abhyuday-fr/Redline/internal/runner"
	"github.com/abhyuday-fr/Redline/internal/scaffold"
	"github.com/abhyuday-fr/Redline/internal/todo"
)

// Execute is the entrypoint called from cmd/redline/main.go.
func Execute(args []string) int {
	if len(args) < 1 {
		printUsage()
		return 1
	}

	cmd := args[0]
	rest := args[1:]

	var err error
	switch cmd {
	case "rev":
		err = runRev(rest)
	case "build":
		err = runBuild(rest)
	case "run":
		err = runRun(rest)
	case "test":
		err = runTest(rest)
	case "add":
		err = runAdd(rest)
	case "clean":
		err = runClean(rest)
	case "doctor":
		err = runDoctor(rest)
	case "todo":
		err = runTodo(rest)
	case "-h", "--help", "help":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		printUsage()
		return 1
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func printUsage() {
	fmt.Fprint(os.Stderr, `redline — a build tool for C++ on Linux/Unix

Usage:
  redline rev [name] [--vcs=none]     Initialize a new project
  redline build [--release]           Compile the project
  redline run [--release] [--bin X]   Build and run
                [--gdb | --valgrind | --perf[=fast|detailed|max] [--flame]]
  redline test [--release]            Run tests
  redline add <dep>[@version] [--dev] Add a dependency
  redline clean                       Remove build artifacts
  redline doctor                      Check for gdb/valgrind/perf/cmake/ninja
  redline todo [--markers=A,B] [dir]  List TODO/FIXME/etc comments
`)
}

func loadManifestFromCwd() (string, *manifest.Manifest, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", nil, err
	}
	m, err := manifest.Load(dir)
	if err != nil {
		return "", nil, fmt.Errorf("not a redline project (%w) — run `redline rev` first", err)
	}
	return dir, m, nil
}

func parseProfile(release bool) cmake.Profile {
	if release {
		return cmake.ProfileRelease
	}
	return cmake.ProfileDev
}

func runRev(args []string) error {
	fs := newFlagSet("rev")
	skipGit := fs.Bool("vcs-none", false, "skip git init")
	libFlag := fs.Bool("lib", false, "create a library project (not supported in v1)")
	if err := parseFlexible(fs, args); err != nil {
		return err
	}
	if *libFlag {
		return fmt.Errorf("--lib is not supported yet — v1 supports binary projects only")
	}

	name := ""
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
		// The project name feeds the CMake target name and must be a bare
		// identifier, not a path — confirmed that passing a path straight
		// through (e.g. `redline rev /tmp/myproject` or `redline rev
		// nested/dir`) breaks CMake configuration, since a target name
		// containing slashes is invalid.
		name = baseName(dir)
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		name = baseName(cwd)
	}

	if err := scaffold.Rev(dir, name, scaffold.Options{SkipGit: *skipGit}); err != nil {
		return err
	}
	fmt.Printf("initialized redline project %q in %s\n", name, dir)
	return nil
}

func runBuild(args []string) error {
	fs := newFlagSet("build")
	release := fs.Bool("release", false, "use the release profile")
	forceRegen := fs.Bool("force-regen", false, "reset the managed CMakeLists.txt block if markers are missing/mangled")
	if err := parseFlexible(fs, args); err != nil {
		return err
	}

	dir, m, err := loadManifestFromCwd()
	if err != nil {
		return err
	}
	profile := parseProfile(*release)
	if err := runner.Build(dir, m, profile, *forceRegen); err != nil {
		return err
	}
	fmt.Println("build finished:", runner.BuildDir(dir, profile))
	return nil
}

func runRun(args []string) error {
	fs := newFlagSet("run")
	release := fs.Bool("release", false, "use the release profile")
	binName := fs.String("bin", "", "which binary to run (default: project's main binary)")
	gdb := fs.Bool("gdb", false, "run under gdb with auto-backtrace on crash")
	valgrind := fs.Bool("valgrind", false, "run under valgrind memcheck")
	perf := fs.String("perf", "", "profile with perf: fast, detailed, or max sampling")
	flame := fs.Bool("flame", false, "generate a flamegraph (implies --perf if not already set)")
	if err := parseFlexible(fs, args); err != nil {
		return err
	}

	dir, m, err := loadManifestFromCwd()
	if err != nil {
		return err
	}
	profile := parseProfile(*release)

	flags := runner.RunFlags{
		Gdb:      *gdb,
		Valgrind: *valgrind,
		PerfMode: *perf,
		Flame:    *flame,
	}
	if *flame && flags.PerfMode == "" {
		flags.PerfMode = "fast"
	}

	return runner.Run(dir, m, profile, *binName, flags)
}

func runTest(args []string) error {
	// v1 convention: tests/main.cpp, mirroring src/main.cpp for the
	// default binary — no manifest entry required, no scanning for main().
	fs := newFlagSet("test")
	release := fs.Bool("release", false, "use the release profile")
	if err := parseFlexible(fs, args); err != nil {
		return err
	}

	dir, m, err := loadManifestFromCwd()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "tests", "main.cpp")); err != nil {
		return fmt.Errorf("no tests/main.cpp found — create one to enable `redline test`")
	}
	profile := parseProfile(*release)
	return runner.Run(dir, m, profile, "tests", runner.RunFlags{})
}

func runAdd(args []string) error {
	fs := newFlagSet("add")
	dev := fs.Bool("dev", false, "add as a dev-only (test) dependency")
	git := fs.String("git", "", "git repository URL for FetchContent (required in v1 — no registry yet)")
	linkTarget := fs.String("link-target", "", "override the CMake target to link (default: <name>::<name> — not all libraries follow this, e.g. Catch2 exports Catch2::Catch2WithMain)")
	if err := parseFlexible(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: redline add <name>[@version] [--git <url>] [--dev]")
	}
	if *git == "" {
		return fmt.Errorf("--git <url> is required in v1 (no dependency registry yet — see design notes)")
	}

	spec := fs.Arg(0)
	name, version := spec, ""
	if idx := strings.LastIndex(spec, "@"); idx != -1 {
		name, version = spec[:idx], spec[idx+1:]
	}
	if version == "" {
		return fmt.Errorf(
			"a version/tag is required — use `redline add %s@<tag>`. "+
				"An unpinned dependency isn't reproducible, and CMake's FetchContent "+
				"falls back to checking out a branch literally named \"master\" when "+
				"no tag is given, which fails on any repo whose default branch is named "+
				"something else (e.g. Catch2 uses \"devel\")", name)
	}

	dir, m, err := loadManifestFromCwd()
	if err != nil {
		return err
	}
	if m.Dependencies == nil {
		m.Dependencies = map[string]manifest.Dependency{}
	}
	m.Dependencies[name] = manifest.Dependency{
		Version:    version,
		Dev:        *dev,
		Git:        *git,
		Tag:        version,
		LinkTarget: *linkTarget,
	}
	if err := m.Save(dir); err != nil {
		return err
	}
	fmt.Printf("added %s to redline.toml (regenerates on next build/run)\n", name)
	return nil
}

func runClean(args []string) error {
	dir, _, err := loadManifestFromCwd()
	if err != nil {
		return err
	}
	if err := runner.Clean(dir); err != nil {
		return err
	}
	fmt.Println("cleaned build artifacts")
	return nil
}

func runDoctor(args []string) error {
	return doctor.Run(doctor.ConfirmStdin)
}

func runTodo(args []string) error {
	fs := newFlagSet("todo")
	markersFlag := fs.String("markers", "", "comma-separated marker words to look for (default: TODO,FIXME,XXX,HACK,BUG,NOTE,OPTIMIZE,REVIEW)")
	if err := parseFlexible(fs, args); err != nil {
		return err
	}

	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}

	var markers []string
	if *markersFlag != "" {
		for _, m := range strings.Split(*markersFlag, ",") {
			m = strings.TrimSpace(m)
			if m != "" {
				markers = append(markers, m)
			}
		}
	}

	matches, err := todo.Scan(dir, markers)
	if err != nil {
		return fmt.Errorf("scanning %s: %w", dir, err)
	}

	if len(matches) == 0 {
		fmt.Println("no pending markers found")
		return nil
	}

	counts := map[string]int{}
	files := map[string]bool{}
	for _, m := range matches {
		fmt.Printf("%s:%d: %s: %s\n", m.File, m.Line, m.Marker, m.Comment)
		counts[m.Marker]++
		files[m.File] = true
	}

	fmt.Println()
	summary := make([]string, 0, len(counts))
	for _, marker := range todo.DefaultMarkers {
		if n, ok := counts[marker]; ok {
			summary = append(summary, fmt.Sprintf("%d %s", n, marker))
		}
	}
	// Any custom markers not in the default list, appended in whatever
	// order they were found so nothing silently drops from the summary.
	seen := map[string]bool{}
	for _, marker := range todo.DefaultMarkers {
		seen[marker] = true
	}
	for marker, n := range counts {
		if !seen[marker] {
			summary = append(summary, fmt.Sprintf("%d %s", n, marker))
		}
	}
	fmt.Printf("%d found across %d file(s): %s\n", len(matches), len(files), strings.Join(summary, ", "))
	return nil
}
