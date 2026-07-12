#!/bin/sh
# Smoke test harness for redline. Builds redline from source, then
# exercises every command against real fixtures in testdata/ and checks
# real outcomes (exit codes, expected output) rather than just "did it
# not crash".
#
# Usage:
#   ./scripts/smoketest.sh          # run all checks, clean up after
#   KEEP=1 ./scripts/smoketest.sh   # leave the temp project dir behind for inspection
#
# Exit code is 0 only if every check passed.
set -u

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TESTDATA="$REPO_ROOT/testdata"
WORKDIR="$(mktemp -d)"
REDLINE="$WORKDIR/redline-bin"

PASS=0
FAIL=0

pass() { PASS=$((PASS + 1)); echo "  ok   - $1"; }
fail() {
    FAIL=$((FAIL + 1))
    echo "  FAIL - $1"
    if [ -n "${2:-}" ]; then
        echo "  ----- captured output -----"
        echo "$2" | sed 's/^/  | /'
        echo "  ----------------------------"
    fi
}

# assert_exit CMD_DESC EXPECTED_EXIT ACTUAL_EXIT [CAPTURED_OUTPUT]
assert_exit() {
    desc="$1"; expected="$2"; actual="$3"; captured="${4:-}"
    if [ "$expected" = "$actual" ]; then
        pass "$desc (exit $actual)"
    else
        fail "$desc (expected exit $expected, got $actual)" "$captured"
    fi
}

# assert_contains DESC HAYSTACK NEEDLE
assert_contains() {
    desc="$1"; haystack="$2"; needle="$3"
    case "$haystack" in
        *"$needle"*) pass "$desc" ;;
        *) fail "$desc (expected to find: $needle)" "$haystack" ;;
    esac
}

cleanup() {
    if [ "${KEEP:-0}" = "1" ]; then
        echo ""
        echo "KEEP=1 set — leaving workdir at $WORKDIR"
    else
        rm -rf "$WORKDIR"
    fi
}
trap cleanup EXIT

echo "=== building redline from source ==="
( cd "$REPO_ROOT" && go build -o "$REDLINE" ./cmd/redline ) || {
    echo "FATAL: redline failed to build"
    exit 1
}

echo ""
echo "=== rev: scaffolding ==="
PROJECT="$WORKDIR/proj"
"$REDLINE" rev "$PROJECT" --vcs-none >/dev/null 2>&1
[ -f "$PROJECT/redline.toml" ] && pass "redline.toml created" || fail "redline.toml missing"
[ -f "$PROJECT/CMakeLists.txt" ] && pass "CMakeLists.txt created" || fail "CMakeLists.txt missing"
[ -f "$PROJECT/src/main.cpp" ] && pass "src/main.cpp created" || fail "src/main.cpp missing"
[ -d "$PROJECT/.git" ] && fail "--vcs-none was ignored (found .git)" || pass "--vcs-none honored (no .git)"

cd "$PROJECT" || exit 1

echo ""
echo "=== build/run: basic hello world ==="
cp "$TESTDATA/hello.cpp" src/main.cpp
out="$("$REDLINE" run 2>&1)"; code=$?
assert_exit "run: dev profile hello world" 0 "$code" "$out"
assert_contains "run: dev profile prints greeting" "$out" "Hello, world!"

out="$("$REDLINE" run --release 2>&1)"; code=$?
assert_exit "run: release profile hello world" 0 "$code" "$out"

echo ""
echo "=== run --gdb: crash produces backtrace and nonzero exit ==="
mkdir -p src/bin
cp "$TESTDATA/crash.cpp" src/bin/crash.cpp
cat >> redline.toml << 'EOF'
EOF
python3 - << 'PYEOF'
import re
content = open("redline.toml").read()
content = re.sub(r"\[binaries\]\n", "[binaries]\n  [[binaries.extra]]\n    name = \"crash\"\n    path = \"src/bin/crash.cpp\"\n", content, count=1)
open("redline.toml", "w").write(content)
PYEOF
if command -v gdb >/dev/null 2>&1; then
    out="$(timeout 15 "$REDLINE" run --bin crash --gdb 2>&1)"; code=$?
    assert_exit "run --gdb: crash propagates as failure" 1 "$code" "$out"
    assert_contains "run --gdb: backtrace shows crash location" "$out" "crash.cpp"
    out="$(timeout 15 "$REDLINE" run --gdb 2>&1)"; code=$?
    assert_exit "run --gdb: normal binary still exits 0" 0 "$code" "$out"
else
    echo "  skip - gdb not installed"
fi

echo ""
echo "=== run --valgrind: rejected on sanitized dev profile, works on release ==="
if command -v valgrind >/dev/null 2>&1; then
    out="$("$REDLINE" run --valgrind 2>&1)"; code=$?
    assert_exit "run --valgrind: dev profile rejected" 1 "$code" "$out"
    assert_contains "run --valgrind: explains the ASan conflict" "$out" "sanitizers enabled"

    out="$("$REDLINE" run --release --valgrind 2>&1)"; code=$?
    assert_exit "run --valgrind: release profile works" 0 "$code" "$out"
else
    echo "  skip - valgrind not installed"
fi

echo ""
echo "=== run --perf/--flame: real CPU-bound workload ==="
cp "$TESTDATA/burn.cpp" src/bin/burn.cpp
python3 - << 'PYEOF'
import re
content = open("redline.toml").read()
content = re.sub(r"\[binaries\]\n", "[binaries]\n  [[binaries.extra]]\n    name = \"burn\"\n    path = \"src/bin/burn.cpp\"\n", content, count=1)
open("redline.toml", "w").write(content)
PYEOF
if command -v perf >/dev/null 2>&1 && perf --version >/dev/null 2>&1; then
    out="$("$REDLINE" run --release --bin burn --perf=max --flame 2>&1)"; code=$?
    assert_exit "run --flame: builds and profiles successfully" 0 "$code" "$out"
    # pipeChain creates the output file before the pipeline runs, so mere
    # existence is a false positive if the pipeline failed immediately —
    # check for real SVG content instead.
    if [ -s build/release/flamegraph.svg ] && grep -q "<svg" build/release/flamegraph.svg 2>/dev/null; then
        pass "run --flame: flamegraph.svg contains real SVG content"
    else
        fail "run --flame: flamegraph.svg missing or empty" "$out"
    fi
else
    echo "  skip - perf not available/working on this system"
fi

echo ""
echo "=== add: requires an explicit version ==="
out="$("$REDLINE" add fmt --git https://github.com/fmtlib/fmt.git 2>&1)"; code=$?
assert_exit "add: bare name without version is rejected" 1 "$code" "$out"
assert_contains "add: explains why a version is required" "$out" "version"

echo ""
echo "=== add + test: real Catch2 suite, pass and fail paths ==="
"$REDLINE" add catch2@v3.5.4 --git https://github.com/catchorg/Catch2.git \
    --dev --link-target Catch2::Catch2WithMain >/dev/null 2>&1
mkdir -p tests

cp "$TESTDATA/test_pass.cpp" tests/main.cpp
out="$("$REDLINE" test 2>&1)"; code=$?
assert_exit "test: passing Catch2 suite exits 0" 0 "$code" "$out"
assert_contains "test: reports assertions passed" "$out" "All tests passed"

cp "$TESTDATA/test_fail.cpp" tests/main.cpp
out="$("$REDLINE" test 2>&1)"; code=$?
assert_exit "test: failing Catch2 suite exits nonzero" 1 "$code" "$out"
assert_contains "test: shows the failed assertion" "$out" "FAILED"

echo ""
echo "=== doctor: runs without crashing, declines install ==="
out="$(echo "n" | "$REDLINE" doctor 2>&1)"; code=$?
assert_exit "doctor: exits 0 after declining install" 0 "$code" "$out"
assert_contains "doctor: checks cmake" "$out" "cmake"

echo ""
echo "=== clean: removes build artifacts ==="
"$REDLINE" clean >/dev/null 2>&1
if [ -d build/dev ] || [ -d build/release ]; then
    fail "clean: build directories still present"
else
    pass "clean: build directories removed"
fi

echo ""
echo "=== summary ==="
echo "  $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
