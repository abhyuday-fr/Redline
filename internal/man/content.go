package man

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed content/*/*.toml
var contentFS embed.FS

type Entry struct {
	Name        string   `toml:"name"`
	Header      string   `toml:"header"`
	Summary     string   `toml:"summary"`
	Synopsis    string   `toml:"synopsis"`
	Description string   `toml:"description"`
	Example     string   `toml:"example"`
	Notes       string   `toml:"notes"`
	SeeAlso     []string `toml:"see_also"`
	Ref         string   `toml:"ref"`

	// filled by the loader, not the TOML file.
	Standard string `toml:"-"`
	Slug     string `toml:"-"`
}

func (e Entry) StdLabel() string { return "C++" + strings.TrimPrefix(e.Standard, "cpp") }

func loadEntries() ([]Entry, error) {
	var out []Entry
	err := fs.WalkDir(contentFS, "content", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".toml") {
			return err
		}
		data, err := contentFS.ReadFile(p)
		if err != nil {
			return err
		}
		var e Entry
		if _, err := toml.Decode(string(data), &e); err != nil {
			return fmtErr(p, err)
		}
		e.Standard = path.Base(path.Dir(p))
		e.Slug = strings.TrimSuffix(path.Base(p), ".toml")
		out = append(out, e)
		return nil
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Standard != out[j].Standard {
			return out[i].Standard < out[j].Standard
		}
		return out[i].Name < out[j].Name
	})
	return out, err
}

func fmtErr(p string, err error) error {
	return fmt.Errorf("%s: %w", p, err)
}
