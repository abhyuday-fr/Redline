// Package manifest handles reading and writing redline.toml, the project
// manifest that drives scaffolding, CMakeLists generation, and the CLI.
package manifest

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Manifest mirrors the on-disk redline.toml schema.
type Manifest struct {
	Project      Project               `toml:"project"`
	Binaries     Binaries              `toml:"binaries"`
	Dependencies map[string]Dependency `toml:"dependencies"`
	Profiles     Profiles              `toml:"profiles"`
	Tools        Tools                 `toml:"tools"`
}

type Project struct {
	Name    string `toml:"name"`
	Version string `toml:"version"`
	Edition string `toml:"edition"` // e.g. "cpp20"
}

type Binaries struct {
	Extra []ExtraBinary `toml:"extra"`
}

type ExtraBinary struct {
	Name string `toml:"name"`
	Path string `toml:"path"`
}

// Dependency supports both the short form (version = "1.2.3") and the
// long form ({ version = "1.2.3", dev = true }) via TOML's flexible
// decoding — BurntSushi/toml will populate Version either way; Dev
// defaults to false when omitted.
type Dependency struct {
	Version string `toml:"version"`
	Dev     bool   `toml:"dev"`
	Git     string `toml:"git,omitempty"` // optional: override registry with a direct git URL
	Tag     string `toml:"tag,omitempty"`
	// LinkTarget overrides the CMake target linked via target_link_libraries.
	// Defaults to `<name>::<name>` when empty, which works for many
	// libraries (e.g. fmt::fmt) but not all — confirmed by testing that
	// Catch2 exports Catch2::Catch2WithMain, an entirely different name
	// with a different casing and a suffix unrelated to the package name.
	// There is no reliable way to guess this from the package name alone.
	LinkTarget string `toml:"link_target,omitempty"`
}

type Profiles struct {
	Dev     Profile `toml:"dev"`
	Release Profile `toml:"release"`
}

type Profile struct {
	Optimization int      `toml:"optimization"` // maps to -O<n>
	DebugSymbols bool     `toml:"debug_symbols"`
	Sanitizers   []string `toml:"sanitizers"` // e.g. ["address", "undefined"]
	GdbOnCrash   bool     `toml:"gdb_on_crash"`
	LTO          bool     `toml:"lto"`
}

type Tools struct {
	Gdb      bool `toml:"gdb"`
	Valgrind bool `toml:"valgrind"`
	Perf     bool `toml:"perf"`
}

// FileName is the manifest's canonical filename in a project root.
const FileName = "redline.toml"

// Load reads and parses redline.toml from the given project directory.
func Load(projectDir string) (*Manifest, error) {
	path := filepath.Join(projectDir, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var m Manifest
	if _, err := toml.Decode(string(data), &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &m, nil
}

// Save writes the manifest back to redline.toml in the given project directory.
func (m *Manifest) Save(projectDir string) error {
	path := filepath.Join(projectDir, FileName)
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()

	enc := toml.NewEncoder(f)
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	return nil
}

// Default returns a fresh Manifest for a newly-scaffolded project,
// matching the design doc's default dev/release profiles.
func Default(projectName string) *Manifest {
	return &Manifest{
		Project: Project{
			Name:    projectName,
			Version: "0.1.0",
			Edition: "cpp20",
		},
		Dependencies: map[string]Dependency{},
		Profiles: Profiles{
			Dev: Profile{
				Optimization: 0,
				DebugSymbols: true,
				Sanitizers:   []string{"address", "undefined"},
				GdbOnCrash:   true,
			},
			Release: Profile{
				Optimization: 3,
				DebugSymbols: false,
				Sanitizers:   []string{},
				LTO:          true,
			},
		},
		Tools: Tools{
			Gdb:      true,
			Valgrind: true,
			Perf:     true,
		},
	}
}
