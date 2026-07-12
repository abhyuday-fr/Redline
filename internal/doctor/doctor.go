// Package doctor implements `redline doctor`: detecting required and
// optional tools (cmake, ninja, git, gdb, valgrind, perf, a flamegraph
// renderer), detecting the host's package manager, and prompting before
// running any install command. Never runs anything system-modifying
// without an explicit yes from the user.
package doctor

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Severity distinguishes hard requirements from optional debug/profiling
// tools that degrade gracefully when absent.
type Severity int

const (
	Required Severity = iota
	Optional
)

// CheckResult is the outcome of probing for one tool.
type CheckResult struct {
	Name     string
	Severity Severity
	OK       bool
	Detail   string // version string on success, or a short reason on failure
}

// checkFn actually invokes the tool to confirm it runs, not just that a
// same-named file exists on PATH — confirmed necessary during testing,
// since `perf` can exist on PATH via a broken version-dispatch wrapper
// and still fail every real invocation.
type checkFn func() (ok bool, detail string)

type toolDef struct {
	name     string
	severity Severity
	check    checkFn
	// packages maps a distro family (see DetectFamily) to the package
	// name(s) needed for this tool, when they differ from the tool name.
	packages map[string][]string
	// noPackage marks tools with no real distro package at all (e.g. the
	// flamegraph renderer, which is either a cargo crate or a standalone
	// git-cloned Perl script) — these must never be folded into the
	// batched package-manager install command, since there is no such
	// package and doing so would silently produce a broken command
	// (confirmed during testing: naively including it produced
	// `apt install ... flamegraph renderer`, treating each word of the
	// tool's description as if it were an installable package name).
	noPackage  bool
	manualHint string
}

var tools = []toolDef{
	{name: "git", severity: Required, check: versionCheck("git", "--version")},
	{name: "cmake", severity: Required, check: cmakeCheck},
	{name: "ninja", severity: Required, check: versionCheck("ninja", "--version"),
		packages: map[string][]string{"apt": {"ninja-build"}}},
	{name: "gdb", severity: Optional, check: versionCheck("gdb", "--version")},
	{name: "valgrind", severity: Optional, check: versionCheck("valgrind", "--version")},
	{name: "sanitizer runtime (ASan/UBSan)", severity: Optional, check: sanitizerCheck,
		noPackage: true,
		manualHint: "install your distro's ASan/UBSan runtime package, " +
			"on Fedora/RHEL: `sudo dnf install libasan libubsan`; on Debian/Ubuntu " +
			"this normally ships bundled with gcc already; package names vary " +
			"elsewhere, search your package manager for \"libasan\""},
	{name: "perf", severity: Optional, check: perfCheck,
		packages: map[string][]string{
			"apt":    {"linux-tools-common", "linux-tools-generic"},
			"dnf":    {"perf"},
			"pacman": {"perf"},
			"zypper": {"perf"},
		}},
	{name: "flamegraph renderer", severity: Optional, check: flamegraphCheck,
		noPackage: true,
		manualHint: "install either inferno (`cargo install inferno`) or clone " +
			"github.com/brendangregg/FlameGraph and put flamegraph.pl + " +
			"stackcollapse-perf.pl on PATH"},
}

func versionCheck(bin string, args ...string) checkFn {
	return func() (bool, string) {
		out, err := exec.Command(bin, args...).CombinedOutput()
		if err != nil {
			return false, "not found"
		}
		line := strings.SplitN(string(out), "\n", 2)[0]
		return true, strings.TrimSpace(line)
	}
}

func cmakeCheck() (bool, string) {
	out, err := exec.Command("cmake", "--version").CombinedOutput()
	if err != nil {
		return false, "not found"
	}
	line := strings.SplitN(string(out), "\n", 2)[0]
	// line looks like "cmake version 3.28.3"
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return true, strings.TrimSpace(line) // can't parse version, assume ok
	}
	major, minor := 0, 0
	fmt.Sscanf(fields[2], "%d.%d", &major, &minor)
	if major < 3 || (major == 3 && minor < 20) {
		return false, fmt.Sprintf("found %s, but redline needs >= 3.20", fields[2])
	}
	return true, strings.TrimSpace(line)
}

// perfCheck actually runs `perf --version` rather than just checking
// exec.LookPath, since Ubuntu's /usr/bin/perf is a version-dispatch
// wrapper that can exist on PATH and still fail every invocation when no
// linux-tools package matches the running kernel exactly — confirmed
// during testing, where this produced a confusing raw error instead of
// an actionable one.
func perfCheck() (bool, string) {
	out, err := exec.Command("perf", "--version").CombinedOutput()
	if err == nil {
		return true, strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	}

	outStr := string(out)
	if strings.Contains(outStr, "not found for kernel") {
		kernel := "your running kernel"
		if v, verr := exec.Command("uname", "-r").Output(); verr == nil {
			kernel = strings.TrimSpace(string(v))
		}
		return false, fmt.Sprintf(
			"perf is on PATH but no linux-tools package matches kernel %s "+
				"(this is common on rolling/custom kernels or in containers), "+
				"check `apt list linux-tools-*` for an available version, or build "+
				"perf from your kernel source", kernel)
	}
	return false, "not found"
}

// sanitizerCheck actually compiles and links a trivial program with
// -fsanitize=address,undefined, rather than just checking that a C++
// compiler exists. Confirmed necessary: a user's Fedora machine had gcc
// installed and working, but linking failed with "cannot find
// libasan.so.8.0.0" because the sanitizer runtime libraries are a
// separate package there — the compiler being present says nothing about
// whether -fsanitize will actually link.
func sanitizerCheck() (bool, string) {
	cxx := "c++"
	if _, err := exec.LookPath(cxx); err != nil {
		cxx = "g++"
	}
	tmpfile, err := os.CreateTemp("", "redline-san-check-*.cpp")
	if err != nil {
		return false, "couldn't create temp file to test"
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.WriteString("int main(){}\n")
	tmpfile.Close()

	outBin, err := os.CreateTemp("", "redline-san-check-bin-*")
	if err != nil {
		return false, "couldn't create temp binary path to test"
	}
	outPath := outBin.Name()
	outBin.Close()
	os.Remove(outPath)
	defer os.Remove(outPath)

	out, err := exec.Command(cxx, "-fsanitize=address,undefined", "-o", outPath, tmpfile.Name()).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "cannot find") {
			return false, "compiler present, but runtime libraries missing (build's -fsanitize link will fail)"
		}
		return false, "compile+link test failed: " + firstLine(msg)
	}
	return true, "verified via compile+link test"
}

func firstLine(s string) string {
	if idx := strings.Index(s, "\n"); idx != -1 {
		return s[:idx]
	}
	return s
}

// sanitizerCheck actually compiles and links a trivial program with
// -fsanitize=address,undefined, rather than just checking that a C++
// compiler exists. Confirmed necessary: a user's Fedora machine had gcc
// installed and working, but linking failed with "cannot find
// libasan.so.8.0.0" because the sanitizer runtime libraries are a
// separate package there — the compiler being present says nothing about
// whether -fsanitize will actually link.
func sanitizerCheck() (bool, string) {
	cxx := "c++"
	if _, err := exec.LookPath(cxx); err != nil {
		cxx = "g++"
	}
	tmpfile, err := os.CreateTemp("", "redline-san-check-*.cpp")
	if err != nil {
		return false, "couldn't create temp file to test"
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.WriteString("int main(){}\n")
	tmpfile.Close()

	outBin, err := os.CreateTemp("", "redline-san-check-bin-*")
	if err != nil {
		return false, "couldn't create temp binary path to test"
	}
	outPath := outBin.Name()
	outBin.Close()
	os.Remove(outPath)
	defer os.Remove(outPath)

	out, err := exec.Command(cxx, "-fsanitize=address,undefined", "-o", outPath, tmpfile.Name()).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "cannot find") {
			return false, "compiler present, but runtime libraries missing (build's -fsanitize link will fail)"
		}
		return false, "compile+link test failed: " + firstLine(msg)
	}
	return true, "verified via compile+link test"
}

func firstLine(s string) string {
	if idx := strings.Index(s, "\n"); idx != -1 {
		return s[:idx]
	}
	return s
}

func flamegraphCheck() (bool, string) {
	if _, err := exec.LookPath("inferno-flamegraph"); err == nil {
		return true, "inferno-flamegraph"
	}
	_, err1 := exec.LookPath("stackcollapse-perf.pl")
	_, err2 := exec.LookPath("flamegraph.pl")
	if err1 == nil && err2 == nil {
		return true, "flamegraph.pl + stackcollapse-perf.pl"
	}
	return false, "neither inferno-flamegraph nor flamegraph.pl+stackcollapse-perf.pl found"
}

// perfParanoidCheck reports the current kernel.perf_event_paranoid value.
// A value greater than 1 blocks unprivileged perf sampling. This is a
// kernel security setting, not a package — never auto-changed, only
// suggested.
func perfParanoidCheck() (level int, ok bool) {
	data, err := os.ReadFile("/proc/sys/kernel/perf_event_paranoid")
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return n, true
}

// Family identifies a package-manager family detected from /etc/os-release.
type Family string

const (
	FamilyAPT     Family = "apt"
	FamilyDNF     Family = "dnf"
	FamilyPacman  Family = "pacman"
	FamilyZypper  Family = "zypper"
	FamilyUnknown Family = ""
)

// DetectFamily parses /etc/os-release's ID and ID_LIKE fields to guess the
// host's package manager family. Returns FamilyUnknown if nothing matches
// rather than guessing — an incorrect guessed install command is worse
// than admitting we don't know.
func DetectFamily() Family {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return FamilyUnknown
	}

	fields := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		idx := strings.Index(line, "=")
		if idx == -1 {
			continue
		}
		key := line[:idx]
		val := strings.Trim(line[idx+1:], `"`)
		fields[key] = val
	}

	haystack := fields["ID"] + " " + fields["ID_LIKE"]
	switch {
	case containsAny(haystack, "ubuntu", "debian"):
		return FamilyAPT
	case containsAny(haystack, "fedora", "rhel"):
		return FamilyDNF
	case containsAny(haystack, "arch", "manjaro"):
		return FamilyPacman
	case containsAny(haystack, "opensuse", "suse"):
		return FamilyZypper
	default:
		return FamilyUnknown
	}
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

func installCommand(family Family, packages []string) string {
	switch family {
	case FamilyAPT:
		return "sudo apt install " + strings.Join(packages, " ")
	case FamilyDNF:
		return "sudo dnf install " + strings.Join(packages, " ")
	case FamilyPacman:
		return "sudo pacman -S " + strings.Join(packages, " ")
	case FamilyZypper:
		return "sudo zypper install " + strings.Join(packages, " ")
	default:
		return ""
	}
}

func packagesFor(t toolDef, family Family) []string {
	if pkgs, ok := t.packages[string(family)]; ok {
		return pkgs
	}
	return []string{t.name}
}

// Run executes all checks, prints a report, and — only if the user
// explicitly confirms — runs the install command for missing tools.
// confirm is called with the exact command that would run; it must
// return true only on explicit affirmative input. Returning false or an
// unrecognized family both result in no system changes.
func Run(confirm func(command string) bool) error {
	fmt.Println("Checking build tools...")
	var missingRequired, missingOptional []toolDef
	var results []CheckResult

	for _, t := range tools {
		ok, detail := t.check()
		results = append(results, CheckResult{Name: t.name, Severity: t.severity, OK: ok, Detail: detail})
		mark := "✓"
		if !ok {
			mark = "✗"
			if t.severity == Required {
				missingRequired = append(missingRequired, t)
			} else {
				missingOptional = append(missingOptional, t)
			}
		}
		fmt.Printf("  %s %-22s %s\n", mark, t.name, detail)
	}

	if level, ok := perfParanoidCheck(); ok && level > 1 {
		fmt.Printf("\n  ! kernel.perf_event_paranoid=%d blocks unprivileged perf sampling\n", level)
		fmt.Println("    fix (not applied automatically): sudo sysctl kernel.perf_event_paranoid=1")
		fmt.Println("    to persist: echo 'kernel.perf_event_paranoid=1' | sudo tee /etc/sysctl.d/99-perf.conf")
	}

	missing := append(append([]toolDef{}, missingRequired...), missingOptional...)
	if len(missing) == 0 {
		fmt.Println("\nAll tools found.")
		return nil
	}

	var installable, manualOnly []toolDef
	for _, t := range missing {
		if t.noPackage {
			manualOnly = append(manualOnly, t)
		} else {
			installable = append(installable, t)
		}
	}

	if len(manualOnly) > 0 {
		fmt.Println("\nThese need manual setup (no distro package available):")
		for _, t := range manualOnly {
			fmt.Printf("  - %s: %s\n", t.name, t.manualHint)
		}
	}

	if len(installable) == 0 {
		return nil
	}

	family := DetectFamily()
	fmt.Printf("\n%d tool(s) missing.", len(installable))
	if family == FamilyUnknown {
		fmt.Println(" Couldn't detect your package manager. Please install these manually:")
		for _, t := range installable {
			fmt.Println("  -", t.name)
		}
		return nil
	}
	fmt.Printf(" Detected package manager: %s\n", family)

	var allPackages []string
	for _, t := range installable {
		allPackages = append(allPackages, packagesFor(t, family)...)
	}
	cmdStr := installCommand(family, allPackages)
	fmt.Println("\n  " + cmdStr)

	if !confirm(cmdStr) {
		fmt.Println("Skipped. Missing tools' corresponding `redline run` flags won't work until installed.")
		return nil
	}

	return runShell(cmdStr)
}

func runShell(cmdStr string) error {
	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// ConfirmStdin is the default confirm callback for interactive use: prints
// "Install now? [y/N]: " and requires an explicit "y"/"yes" (case
// insensitive) — anything else, including a bare Enter, is treated as no.
func ConfirmStdin(command string) bool {
	fmt.Print("Install now? [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}
