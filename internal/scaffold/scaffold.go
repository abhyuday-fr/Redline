// Package scaffold implements project initialization: git init, directory
// layout, default source files, and the initial manifest + CMakeLists.txt.
package scaffold

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/lawless/redline/internal/cmake"
	"github.com/lawless/redline/internal/manifest"
)

// Options controls what Rev (the init routine) does.
type Options struct {
	// SkipGit disables `git init`, for --vcs=none.
	SkipGit bool
}

const helloWorldMain = `#include <iostream>

int main() {
    std::cout << "Hello, world!" << std::endl;
    return 0;
}
`

const gitignore = `build/
.engine/
.redline/
*.o
*.out
`

// Rev initializes a new redline project at dir. dir must either not exist
// (it will be created) or be empty. projectName is used both as the
// directory name (if newly created) and the [project] name in redline.toml.
func Rev(dir, projectName string, opts Options) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating project directory: %w", err)
	}

	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return fmt.Errorf("creating src/: %w", err)
	}

	mainPath := filepath.Join(srcDir, "main.cpp")
	if _, err := os.Stat(mainPath); os.IsNotExist(err) {
		if err := os.WriteFile(mainPath, []byte(helloWorldMain), 0o644); err != nil {
			return fmt.Errorf("writing src/main.cpp: %w", err)
		}
	}

	gitignorePath := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		if err := os.WriteFile(gitignorePath, []byte(gitignore), 0o644); err != nil {
			return fmt.Errorf("writing .gitignore: %w", err)
		}
	}

	m := manifest.Default(projectName)
	if _, err := os.Stat(filepath.Join(dir, manifest.FileName)); os.IsNotExist(err) {
		if err := m.Save(dir); err != nil {
			return fmt.Errorf("writing %s: %w", manifest.FileName, err)
		}
	}

	// Initial CMakeLists.txt with an empty-but-valid managed block.
	cmakePath := filepath.Join(dir, "CMakeLists.txt")
	if _, err := os.Stat(cmakePath); os.IsNotExist(err) {
		if err := cmake.Generate(dir, m, cmake.ProfileDev); err != nil {
			return fmt.Errorf("generating CMakeLists.txt: %w", err)
		}
	}

	if !opts.SkipGit {
		if err := gitInit(dir); err != nil {
			// Non-fatal: a missing git binary or an already-initialized repo
			// shouldn't block scaffolding the rest of the project.
			fmt.Fprintf(os.Stderr, "warning: git init skipped: %v\n", err)
		}
	}

	return nil
}

func gitInit(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return nil // already a git repo, nothing to do
	}
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
