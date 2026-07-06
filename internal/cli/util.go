package cli

import (
	"flag"
	"path/filepath"
	"strings"
)

// newFlagSet creates a flag.FlagSet configured to print usage on error
// via the standard flag package behavior (os.Exit is avoided by using
// ContinueOnError so our own error handling in Execute stays in control).
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	return fs
}

// baseName returns the last path component, used to default a project's
// name to its containing directory name when `redline rev` is run with
// no explicit name argument.
func baseName(path string) string {
	return filepath.Base(path)
}

// parseFlexible parses args against fs, tolerating flags that appear
// after positional arguments (e.g. `redline rev myproject --vcs-none`).
// Go's stdlib flag package stops parsing at the first non-flag token,
// which would silently drop --vcs-none as a bogus positional arg instead
// of applying it — that's a correctness bug for a CLI where flag order
// after a project name is the natural way to type a command. This
// reorders args so recognized flags come first, then delegates to
// fs.Parse for the actual value parsing/validation.
//
// All flags must be registered on fs before calling this.
func parseFlexible(fs *flag.FlagSet, args []string) error {
	var flagArgs, positional []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}

		name := strings.TrimLeft(a, "-")
		hasInlineValue := false
		if idx := strings.Index(name, "="); idx != -1 {
			name = name[:idx]
			hasInlineValue = true
		}

		f := fs.Lookup(name)
		if f == nil {
			// Unrecognized flag: let fs.Parse produce the real error message
			// rather than silently swallowing it as positional.
			flagArgs = append(flagArgs, a)
			continue
		}

		flagArgs = append(flagArgs, a)

		// If it's not a bool flag and the value wasn't given as --name=value,
		// the next token is its value and must travel with it.
		isBool := false
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok {
			isBool = bf.IsBoolFlag()
		}
		if !isBool && !hasInlineValue && i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}

	return fs.Parse(append(flagArgs, positional...))
}
