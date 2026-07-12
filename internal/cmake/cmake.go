// Package cmake generates and safely regenerates the "managed block" inside
// a project's CMakeLists.txt from the redline.toml manifest. Everything
// outside the managed block is preserved verbatim across regenerations —
// this is the core trust boundary of the whole tool: users can hand-edit
// CMakeLists.txt freely as long as they don't edit inside the markers.
package cmake

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lawless/redline/internal/manifest"
)

// Profile selects which [profiles.*] table from the manifest feeds the
// generated managed block.
type Profile string

const (
	ProfileDev     Profile = "dev"
	ProfileRelease Profile = "release"
)

const beginMarker = "# === ENGINE:BEGIN (auto-generated, do not edit between markers, changes will be overwritten) ==="
const endMarker = "# === ENGINE:END ==="

// ErrMarkersNotFound is returned when an existing CMakeLists.txt doesn't
// contain both markers in order. Callers should surface this to the user
// and require an explicit --force-regen rather than guessing.
type ErrMarkersNotFound struct{ Path string }

func (e *ErrMarkersNotFound) Error() string {
	return fmt.Sprintf(
		"couldn't find managed markers in %s. Has it been edited? "+
			"run with --force-regen to reset the managed block, or restore the markers manually",
		e.Path,
	)
}

// Generate writes (or safely regenerates) CMakeLists.txt in projectDir.
func Generate(projectDir string, m *manifest.Manifest, profile Profile) error {
	return generate(projectDir, m, profile, false)
}

// GenerateForce is Generate but proceeds even if existing markers are
// missing/mangled, discarding whatever was between where markers should be.
// Callers should only reach this after the user has passed --force-regen.
func GenerateForce(projectDir string, m *manifest.Manifest, profile Profile) error {
	return generate(projectDir, m, profile, true)
}

func generate(projectDir string, m *manifest.Manifest, profile Profile, force bool) error {
	path := filepath.Join(projectDir, "CMakeLists.txt")
	block := renderManagedBlock(projectDir, m, profile)

	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// Fresh file: header + managed block + a friendly comment for user code.
		content := renderHeader(m) + "\n" + block + "\n\n" +
			"# Anything below this line is yours. Engine will never touch it.\n"
		return os.WriteFile(path, []byte(content), 0o644)
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	before, after, ok := splitOnMarkers(string(existing))
	if !ok {
		if !force {
			return &ErrMarkersNotFound{Path: path}
		}
		// Force mode with unrecognized markers: keep the whole existing
		// file as trailing user content, prepend a fresh header + block.
		return os.WriteFile(path, []byte(renderHeader(m)+"\n"+block+"\n\n"+string(existing)), 0o644)
	}

	newContent := before + block + after
	return os.WriteFile(path, []byte(newContent), 0o644)
}

// splitOnMarkers finds beginMarker and endMarker (in that order) in content
// and returns everything up to and including beginMarker's line as
// "before", everything from endMarker's line onward as "after", and ok=true.
// If the markers are missing or out of order, ok=false.
func splitOnMarkers(content string) (before, after string, ok bool) {
	beginIdx := strings.Index(content, beginMarker)
	if beginIdx == -1 {
		return "", "", false
	}
	endIdx := strings.Index(content, endMarker)
	if endIdx == -1 || endIdx < beginIdx {
		return "", "", false
	}
	before = content[:beginIdx]
	// Skip past the end marker line itself (including its trailing
	// newline) since the freshly rendered block already writes its own
	// end marker — otherwise it would appear twice.
	afterStart := endIdx + len(endMarker)
	if afterStart < len(content) && content[afterStart] == '\n' {
		afterStart++
	}
	after = content[afterStart:]
	return before, after, true
}

func renderHeader(m *manifest.Manifest) string {
	std := strings.TrimPrefix(m.Project.Edition, "cpp")
	var b strings.Builder
	fmt.Fprintf(&b, "cmake_minimum_required(VERSION 3.20)\n")
	fmt.Fprintf(&b, "project(%s CXX)\n\n", m.Project.Name)
	fmt.Fprintf(&b, "set(CMAKE_CXX_STANDARD %s)\n", std)
	fmt.Fprintf(&b, "set(CMAKE_CXX_STANDARD_REQUIRED ON)\n")
	return b.String()
}

func renderManagedBlock(projectDir string, m *manifest.Manifest, profile Profile) string {
	p := m.Profiles.Dev
	if profile == ProfileRelease {
		p = m.Profiles.Release
	}

	// Split dependencies: regular deps are linked into every real binary;
	// dev-only deps (test frameworks like Catch2) are linked only into
	// the auto-detected `tests` target, never into shipped binaries.
	regularDeps := map[string]manifest.Dependency{}
	devDeps := map[string]manifest.Dependency{}
	for name, dep := range m.Dependencies {
		if dep.Dev {
			devDeps[name] = dep
		} else {
			regularDeps[name] = dep
		}
	}
	allDeps := m.Dependencies

	var b strings.Builder
	b.WriteString(beginMarker + "\n")

	if len(allDeps) > 0 {
		b.WriteString("include(FetchContent)\n\n")
		for _, name := range sortedDepNames(allDeps) {
			dep := allDeps[name]
			fmt.Fprintf(&b, "FetchContent_Declare(%s\n", name)
			if dep.Git != "" {
				fmt.Fprintf(&b, "  GIT_REPOSITORY %s\n", dep.Git)
				tag := dep.Tag
				if tag == "" {
					tag = dep.Version
				}
				if tag != "" {
					fmt.Fprintf(&b, "  GIT_TAG %s\n", tag)
				} else {
					// No tag/version at all: omit GIT_TAG entirely rather than
					// emit an empty value. Confirmed by testing that an empty
					// GIT_TAG makes CMake's FetchContent fall back to checking
					// out a branch literally named "master", which fails on
					// any repo whose default branch has a different name.
					fmt.Fprintf(&b, "  # WARNING: no version/tag pinned for %s, unpinned FetchContent deps aren't reproducible\n", name)
				}
			} else {
				// No explicit git URL: assume a github.com/<name>/<name>-style
				// convention is too fragile to guess, so require Git to be
				// set explicitly for now. This keeps generation honest rather
				// than inventing a URL that might not exist.
				fmt.Fprintf(&b, "  # NOTE: no git source specified for %s, set `git` in redline.toml\n", name)
			}
			fmt.Fprintf(&b, ")\n")
			fmt.Fprintf(&b, "FetchContent_MakeAvailable(%s)\n\n", name)
		}
	}

	// Default binary: src/main.cpp -> target named after the project.
	targetName := m.Project.Name
	fmt.Fprintf(&b, "add_executable(%s src/main.cpp)\n", targetName)
	writeTargetFlags(&b, targetName, p, regularDeps)

	// Extra binaries: src/bin/<name>.cpp
	for _, bin := range m.Binaries.Extra {
		fmt.Fprintf(&b, "\nadd_executable(%s %s)\n", bin.Name, bin.Path)
		writeTargetFlags(&b, bin.Name, p, regularDeps)
	}

	// Tests: tests/main.cpp -> target "tests", auto-detected the same way
	// src/main.cpp is — no manifest entry required. Links both regular
	// and dev-only dependencies, since tests typically need the library
	// under test plus a test framework.
	testsMain := filepath.Join(projectDir, "tests", "main.cpp")
	if _, err := os.Stat(testsMain); err == nil {
		fmt.Fprintf(&b, "\nadd_executable(tests tests/main.cpp)\n")
		writeTargetFlags(&b, "tests", p, allDeps)
	}

	b.WriteString(endMarker + "\n")
	return b.String()
}

func sortedDepNames(deps map[string]manifest.Dependency) []string {
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func writeTargetFlags(b *strings.Builder, target string, p manifest.Profile, deps map[string]manifest.Dependency) {
	if len(deps) > 0 {
		fmt.Fprintf(b, "target_link_libraries(%s PRIVATE", target)
		for _, name := range sortedDepNames(deps) {
			dep := deps[name]
			linkTarget := dep.LinkTarget
			if linkTarget == "" {
				linkTarget = name + "::" + name
			}
			fmt.Fprintf(b, " %s", linkTarget)
		}
		fmt.Fprintf(b, ")\n")
	}

	fmt.Fprintf(b, "target_compile_options(%s PRIVATE -O%d", target, p.Optimization)
	if p.DebugSymbols {
		fmt.Fprintf(b, " -g")
	}
	if len(p.Sanitizers) > 0 {
		fmt.Fprintf(b, " -fsanitize=%s", strings.Join(p.Sanitizers, ","))
	}
	fmt.Fprintf(b, ")\n")

	if len(p.Sanitizers) > 0 {
		fmt.Fprintf(b, "target_link_options(%s PRIVATE -fsanitize=%s)\n", target, strings.Join(p.Sanitizers, ","))
	}

	if p.LTO {
		fmt.Fprintf(b, "set_property(TARGET %s PROPERTY INTERPROCEDURAL_OPTIMIZATION ON)\n", target)
	}
}
