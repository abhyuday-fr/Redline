// Package runner drives the actual cmake/ninja invocations for build and
// run, and wraps execution under gdb/valgrind/perf when requested.
package runner

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lawless/redline/internal/cmake"
	"github.com/lawless/redline/internal/manifest"
)

// BuildDir returns the profile-specific build directory, e.g. build/dev.
func BuildDir(projectDir string, profile cmake.Profile) string {
	return filepath.Join(projectDir, "build", string(profile))
}

// Configure runs `cmake -S . -B build/<profile> -G Ninja` if the build
// directory doesn't already have a cache, so repeat builds don't
// reconfigure unnecessarily.
func Configure(projectDir string, profile cmake.Profile) error {
	buildDir := BuildDir(projectDir, profile)
	cacheFile := filepath.Join(buildDir, "CMakeCache.txt")
	if _, err := os.Stat(cacheFile); err == nil {
		return nil // already configured
	}

	cmakeBuildType := "Debug"
	if profile == cmake.ProfileRelease {
		cmakeBuildType = "Release"
	}

	cmd := exec.Command("cmake", "-S", ".", "-B", buildDir, "-G", "Ninja",
		"-DCMAKE_BUILD_TYPE="+cmakeBuildType)
	cmd.Dir = projectDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cmake configure failed: %w", err)
	}
	return nil
}

// Build regenerates the managed CMakeLists.txt block from the manifest,
// configures if needed, then invokes the actual build.
func Build(projectDir string, m *manifest.Manifest, profile cmake.Profile, forceRegen bool) error {
	var err error
	if forceRegen {
		err = cmake.GenerateForce(projectDir, m, profile)
	} else {
		err = cmake.Generate(projectDir, m, profile)
	}
	if err != nil {
		return err
	}

	if err := Configure(projectDir, profile); err != nil {
		return err
	}

	buildDir := BuildDir(projectDir, profile)
	cmd := exec.Command("cmake", "--build", buildDir)
	cmd.Dir = projectDir
	cmd.Stdin = os.Stdin

	// Tee output to a buffer so we can recognize specific known-cryptic
	// failures (confirmed via a real user report: gcc present and
	// working, but -fsanitize linking failed with a bare "cannot find
	// libasan.so.8.0.0" — meaningless to anyone who doesn't already know
	// sanitizer runtimes are a separate package on their distro) and
	// translate them into an actionable message, while still streaming
	// the raw output live so nothing is hidden.
	var captured bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &captured)
	cmd.Stderr = io.MultiWriter(os.Stderr, &captured)

	if err := cmd.Run(); err != nil {
		if hint := diagnoseBuildFailure(captured.String()); hint != "" {
			return fmt.Errorf("build failed: %w\n\n%s", err, hint)
		}
		return fmt.Errorf("build failed: %w", err)
	}
	return nil
}

// diagnoseBuildFailure recognizes specific known-cryptic linker/compiler
// errors and returns an actionable hint, or "" if nothing recognized.
func diagnoseBuildFailure(output string) string {
	if strings.Contains(output, "cannot find") &&
		(strings.Contains(output, "libasan") || strings.Contains(output, "libubsan") ||
			strings.Contains(output, "libtsan") || strings.Contains(output, "liblsan")) {
		return "hint: this profile has sanitizers enabled, but the sanitizer runtime " +
			"libraries aren't installed on this system (the compiler itself is fine, " +
			"this is a separate package on some distros, e.g. `libasan`/`libubsan` on " +
			"Fedora/RHEL). Run `redline doctor` to check, or use --release to build " +
			"without sanitizers in the meantime."
	}
	return ""
}

// diagnoseBuildFailure recognizes specific known-cryptic linker/compiler
// errors and returns an actionable hint, or "" if nothing recognized.
func diagnoseBuildFailure(output string) string {
	if strings.Contains(output, "cannot find") &&
		(strings.Contains(output, "libasan") || strings.Contains(output, "libubsan") ||
			strings.Contains(output, "libtsan") || strings.Contains(output, "liblsan")) {
		return "hint: this profile has sanitizers enabled, but the sanitizer runtime " +
			"libraries aren't installed on this system (the compiler itself is fine — " +
			"this is a separate package on some distros, e.g. `libasan`/`libubsan` on " +
			"Fedora/RHEL). Run `redline doctor` to check, or use --release to build " +
			"without sanitizers in the meantime."
	}
	return ""
}

// RunFlags controls how the built binary is executed.
type RunFlags struct {
	Gdb      bool
	Valgrind bool
	// PerfMode is empty when profiling isn't requested, otherwise one of
	// "fast", "detailed", "max" (sampling-frequency tiers — see design notes;
	// this is intentionally NOT a latency percentile).
	PerfMode string
	Flame    bool
}

// perfFrequency maps our friendly tiers to `perf record -F` sampling rates.
var perfFrequency = map[string]string{
	"fast":     "99",
	"detailed": "999",
	"max":      "4000",
}

// Run builds (if needed) then executes the named binary, optionally
// wrapped in gdb/valgrind/perf per flags. binName selects which target to
// run; if empty, the project's default binary (same name as [project].name)
// is used.
func Run(projectDir string, m *manifest.Manifest, profile cmake.Profile, binName string, flags RunFlags) error {
	if flags.Gdb && flags.Valgrind {
		return fmt.Errorf("--gdb and --valgrind can't be combined — run them separately")
	}

	if flags.Valgrind {
		p := m.Profiles.Dev
		if profile == cmake.ProfileRelease {
			p = m.Profiles.Release
		}
		if len(p.Sanitizers) > 0 {
			return fmt.Errorf(
				"--valgrind can't be used with the %s profile because it has sanitizers enabled (%s) — "+
					"AddressSanitizer/UBSan and Valgrind instrument memory access in conflicting ways and "+
					"will produce spurious errors, not real ones. Use --release (sanitizer-free by default) "+
					"instead, or disable sanitizers in that profile in redline.toml",
				profile, strings.Join(p.Sanitizers, ", "))
		}
	}

	if err := Build(projectDir, m, profile, false); err != nil {
		return err
	}

	if binName == "" {
		binName = m.Project.Name
	}
	binPath := filepath.Join(BuildDir(projectDir, profile), binName)
	if _, err := os.Stat(binPath); err != nil {
		return fmt.Errorf("built binary not found at %s: %w", binPath, err)
	}

	var cmd *exec.Cmd
	var detectGdbCrash bool

	switch {
	case flags.Gdb:
		// -batch runs the -ex script non-interactively and exits
		// afterward instead of dropping to an interactive gdb prompt
		// (confirmed: without -batch, gdb hangs forever waiting for
		// input once the script finishes). gdb stops automatically
		// when the inferior receives SIGSEGV/SIGABRT/etc. even without
		// an explicit `catch signal`, so `run` then `bt` on the
		// resulting stop is sufficient.
		//
		// NOTE: we do NOT use gdb's if/else/end control flow here to
		// propagate the crash as a nonzero exit — confirmed by direct
		// testing that even a trivial `if 1 / ... / end` block hangs
		// when split across separate -ex flags (gdb expects control-flow
		// bodies as continuous script/stdin input, not discrete -ex
		// arguments). We also can't use $_exitsignal for this: it's
		// only set once the inferior actually terminates, and gdb
		// stopping on a signal leaves the process merely paused, not
		// terminated, so $_exitsignal stays void regardless of whether
		// a crash occurred. Instead we scan gdb's captured output for
		// the crash-report text it reliably prints on a stop.
		cmd = exec.Command("gdb",
			"-batch",
			"-ex", "run",
			"-ex", "bt",
			"--args", binPath)
		// LeakSanitizer (bundled into ASan builds on Linux) refuses to run
		// under any ptrace-based debugger and treats that as a fatal error
		// — confirmed by direct testing: it kills even a completely normal
		// run. This disables only the leak-detection-at-exit component;
		// ASan's other checks (buffer overflow, use-after-free, etc.) and
		// UBSan remain fully active and still catch real bugs under gdb.
		cmd.Env = append(os.Environ(), "ASAN_OPTIONS=detect_leaks=0")
		detectGdbCrash = true

	case flags.Valgrind:
		cmd = exec.Command("valgrind",
			"--leak-check=full",
			"--show-leak-kinds=all",
			binPath)

	case flags.PerfMode != "" || flags.Flame:
		freq, ok := perfFrequency[flags.PerfMode]
		if flags.PerfMode == "" {
			freq = perfFrequency["fast"]
		} else if !ok {
			return fmt.Errorf("unknown --perf mode %q (expected fast, detailed, or max)", flags.PerfMode)
		}

		// Confirmed by direct testing: profiling a sanitizer-instrumented
		// build captures the sanitizer's own runtime overhead (e.g. its
		// exit-time leak scan), not the program's real hot paths. We warn
		// rather than force --release, since optimization/profile choice
		// is deliberately left to the user per the design notes.
		activeProfile := m.Profiles.Dev
		if profile == cmake.ProfileRelease {
			activeProfile = m.Profiles.Release
		}
		if len(activeProfile.Sanitizers) > 0 {
			fmt.Fprintf(os.Stderr,
				"warning: profiling the %s profile, which has sanitizers enabled (%s) — "+
					"the flamegraph may be dominated by sanitizer runtime overhead rather than "+
					"your program's real behavior. Consider --release for a representative profile.\n",
				profile, strings.Join(activeProfile.Sanitizers, ", "))
		}

		perfDataPath := filepath.Join(BuildDir(projectDir, profile), "perf.data")
		cmd = exec.Command("perf", "record", "-F", freq, "-g",
			"-o", perfDataPath, "--", binPath)
		defer func() {
			if flags.Flame {
				if err := generateFlamegraph(projectDir, profile, perfDataPath); err != nil {
					fmt.Fprintf(os.Stderr, "warning: flamegraph generation failed: %v\n", err)
				}
			}
		}()

	default:
		cmd = exec.Command(binPath)
	}

	cmd.Dir = projectDir
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if !detectGdbCrash {
		cmd.Stdout = os.Stdout
		return cmd.Run()
	}

	// Tee stdout to a buffer so we can scan gdb's transcript for the
	// crash-report text after the fact, while still streaming it live.
	var captured bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &captured)

	// gdb's own exit code isn't authoritative here: a harmless sub-command
	// failure (e.g. `bt` reporting "No stack" after the inferior exits
	// normally) makes gdb itself exit nonzero even on a fully successful
	// run — confirmed by direct testing. Judge success/failure purely from
	// whether the transcript shows the inferior actually crashed.
	_ = cmd.Run()
	if crashSignal := detectCrashSignal(captured.String()); crashSignal != "" {
		return fmt.Errorf("program crashed (%s) — see backtrace above", crashSignal)
	}
	return nil
}

// detectCrashSignal scans a gdb batch-mode transcript for the standard
// "Program received signal SIGXXX" line gdb prints when the inferior stops
// due to a fatal signal, and returns the signal name if found, or "" if the
// program appears to have run to completion normally.
func detectCrashSignal(transcript string) string {
	const marker = "Program received signal "
	idx := strings.Index(transcript, marker)
	if idx == -1 {
		return ""
	}
	rest := transcript[idx+len(marker):]
	if commaIdx := strings.Index(rest, ","); commaIdx != -1 {
		return rest[:commaIdx]
	}
	return strings.TrimSpace(rest)
}

func generateFlamegraph(projectDir string, profile cmake.Profile, perfDataPath string) error {
	buildDir := BuildDir(projectDir, profile)
	svgPath := filepath.Join(buildDir, "flamegraph.svg")

	if _, err := exec.LookPath("inferno-flamegraph"); err == nil {
		script := exec.Command("perf", "script", "-i", perfDataPath)
		flame := exec.Command("inferno-flamegraph")
		return pipeChain(svgPath, script, flame)
	}

	// Fall back to the classic Brendan Gregg FlameGraph scripts
	// (stackcollapse-perf.pl + flamegraph.pl) if present on PATH — these
	// are plain Perl scripts with no build step, a reasonable thing to
	// expect a systems programmer to have cloned locally even without
	// inferno installed.
	if _, err := exec.LookPath("stackcollapse-perf.pl"); err == nil {
		if _, err := exec.LookPath("flamegraph.pl"); err == nil {
			script := exec.Command("perf", "script", "-i", perfDataPath)
			collapse := exec.Command("stackcollapse-perf.pl")
			flame := exec.Command("flamegraph.pl")
			return pipeChain(svgPath, script, collapse, flame)
		}
	}

	return fmt.Errorf(
		"no flamegraph renderer found on PATH — install either inferno " +
			"(cargo install inferno) or Brendan Gregg's FlameGraph scripts " +
			"(github.com/brendangregg/FlameGraph, needs stackcollapse-perf.pl " +
			"and flamegraph.pl on PATH); run `redline doctor` to check")
}

// pipeChain runs cmds in sequence, piping each command's stdout into the
// next's stdin, and writes the final command's stdout to outputPath.
func pipeChain(outputPath string, cmds ...*exec.Cmd) error {
	if len(cmds) == 0 {
		return fmt.Errorf("pipeChain: no commands given")
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	for i := 0; i < len(cmds)-1; i++ {
		pipe, err := cmds[i].StdoutPipe()
		if err != nil {
			return err
		}
		cmds[i+1].Stdin = pipe
		cmds[i].Stderr = os.Stderr
	}
	last := cmds[len(cmds)-1]
	last.Stdout = out
	last.Stderr = os.Stderr

	// Start downstream commands first so they're ready to consume as
	// upstream commands produce output, then run the chain left to right.
	for i := len(cmds) - 1; i > 0; i-- {
		if err := cmds[i].Start(); err != nil {
			return fmt.Errorf("starting %s: %w", cmds[i].Path, err)
		}
	}
	if err := cmds[0].Run(); err != nil {
		return fmt.Errorf("running %s: %w", cmds[0].Path, err)
	}
	for i := 1; i < len(cmds); i++ {
		if err := cmds[i].Wait(); err != nil {
			return fmt.Errorf("running %s: %w", cmds[i].Path, err)
		}
	}
	fmt.Println("flamegraph written to", outputPath)
	return nil
}

// Clean removes both profile build directories.
func Clean(projectDir string) error {
	for _, p := range []cmake.Profile{cmake.ProfileDev, cmake.ProfileRelease} {
		if err := os.RemoveAll(BuildDir(projectDir, p)); err != nil {
			return err
		}
	}
	return nil
}
