/* package todo scans C/C++ source files for TODO/FIXME/etc markers left in
* comments, so `redline todo` can give developers a quick "what's pending"
* view across a project.
*
* This does a real lightweight lex of each line to distinguish code,
* string literals, and comments, not a naive regex search, because a
* naive `strings.Contains(line, "//")` or regex match would misfire on
* things like a URL inside a string literal (`"http://example.com"`) or
* an identifier that merely contains a marker word (`kTodoListCapacity`).
 */
package todo

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DefaultMarkers are the marker words scanned for when the caller doesn't
// override the list. Matching is case-insensitive; the marker is reported
// normalized to uppercase.
var DefaultMarkers = []string{"TODO", "FIXME", "XXX", "HACK", "BUG", "NOTE", "OPTIMIZE", "REVIEW"}

// sourceExtensions are the file extensions scanned by default.
var sourceExtensions = map[string]bool{
	".c": true, ".h": true,
	".cc": true, ".hh": true,
	".cpp": true, ".hpp": true,
	".cxx": true, ".hxx": true,
}

// skipDirs are never descended into, regardless of project layout —
// build output and VCS metadata aren't source the developer is tracking
// pending work in.
var skipDirs = map[string]bool{
	"build": true, ".git": true, "_deps": true,
}

// Match is one found marker.
type Match struct {
	File    string // relative to the scan root
	Line    int
	Marker  string // normalized uppercase, e.g. "TODO"
	Comment string // the comment text from the marker onward, trimmed
}

// Scan walks root looking for marker comments in C/C++ source files.
// markers is the set of marker words to look for (case-insensitive);
// pass nil to use DefaultMarkers.
func Scan(root string, markers []string) ([]Match, error) {
	if len(markers) == 0 {
		markers = DefaultMarkers
	}
	markerRE := buildMarkerRegexp(markers)

	var matches []Match
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !sourceExtensions[filepath.Ext(path)] {
			return nil
		}

		fileMatches, err := scanFile(path, root, markerRE)
		if err != nil {
			return fmt.Errorf("scanning %s: %w", path, err)
		}
		matches = append(matches, fileMatches...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].File != matches[j].File {
			return matches[i].File < matches[j].File
		}
		return matches[i].Line < matches[j].Line
	})
	return matches, nil
}

func buildMarkerRegexp(markers []string) *regexp.Regexp {
	escaped := make([]string, len(markers))
	for i, m := range markers {
		escaped[i] = regexp.QuoteMeta(m)
	}
	// Word boundary on both sides so TODO doesn't match inside TODOLIST,
	// case-insensitive so `// todo:` and `// TODO:` both hit.
	return regexp.MustCompile(`(?i)\b(` + strings.Join(escaped, "|") + `)\b`)
}

// commentState tracks lexer state across lines for block comments, since
// a /* ... */ can span many lines.
type commentState struct {
	inBlockComment bool
}

func scanFile(path, root string, markerRE *regexp.Regexp) ([]Match, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	relPath, err := filepath.Rel(root, path)
	if err != nil {
		relPath = path
	}

	var matches []Match
	state := &commentState{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // handle long lines
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		comments := extractComments(line, state)
		for _, c := range comments {
			loc := markerRE.FindStringIndex(c)
			if loc == nil {
				continue
			}
			marker := strings.ToUpper(markerRE.FindString(c))
			comment := strings.TrimSpace(c[loc[0]:])
			matches = append(matches, Match{
				File:    relPath,
				Line:    lineNum,
				Marker:  marker,
				Comment: comment,
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return matches, nil
}

// extractComments returns the comment substrings on a line — the text
// after `//`, or the text inside `/* ... */` (tracking multi-line block
// comments via state), while correctly skipping over string and char
// literals so a `//` or `/*` inside a string isn't mistaken for a real
// comment start. This is a deliberately small lexer, not a full C++
// tokenizer: it doesn't handle raw strings (R"(...)"), digraphs, or
// trigraphs, which is an acceptable gap for a "what's pending" scanner.
func extractComments(line string, state *commentState) []string {
	var comments []string
	i := 0
	n := len(line)

	for i < n {
		if state.inBlockComment {
			end := strings.Index(line[i:], "*/")
			if end == -1 {
				comments = append(comments, line[i:])
				return comments
			}
			comments = append(comments, line[i:i+end])
			i += end + 2
			state.inBlockComment = false
			continue
		}

		c := line[i]
		switch {
		case c == '"':
			j := skipStringLiteral(line, i)
			i = j
		case c == '\'':
			j := skipCharLiteral(line, i)
			i = j
		case c == '/' && i+1 < n && line[i+1] == '/':
			comments = append(comments, line[i+2:])
			return comments
		case c == '/' && i+1 < n && line[i+1] == '*':
			end := strings.Index(line[i+2:], "*/")
			if end == -1 {
				comments = append(comments, line[i+2:])
				state.inBlockComment = true
				return comments
			}
			comments = append(comments, line[i+2:i+2+end])
			i += 2 + end + 2
		default:
			i++
		}
	}
	return comments
}

// skipStringLiteral returns the index just past the closing quote of the
// string literal starting at i (which must point at the opening `"`),
// respecting backslash escapes. If unterminated, returns len(line).
func skipStringLiteral(line string, i int) int {
	n := len(line)
	i++ // skip opening quote
	for i < n {
		if line[i] == '\\' && i+1 < n {
			i += 2
			continue
		}
		if line[i] == '"' {
			return i + 1
		}
		i++
	}
	return n
}

func skipCharLiteral(line string, i int) int {
	n := len(line)
	i++ // skip opening quote
	for i < n {
		if line[i] == '\\' && i+1 < n {
			i += 2
			continue
		}
		if line[i] == '\'' {
			return i + 1
		}
		i++
	}
	return n
}
