package man

import "testing"

func TestEntriesLoad(t *testing.T) {
	entries, err := loadEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no entries embedded")
	}
	for _, e := range entries {
		if e.Name == "" || e.Summary == "" || e.Description == "" {
			t.Errorf("%s/%s: missing name, summary or description", e.Standard, e.Slug)
		}
	}
}
