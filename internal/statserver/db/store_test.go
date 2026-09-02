package db

import (
	"io/fs"
	"sort"
	"strings"
	"testing"
)

func TestMigrationsAreOrderedAndTableScoped(t *testing.T) {
	files, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	if strings.Join(files, "\n") != strings.Join(sorted, "\n") {
		t.Fatal("embedded migrations are not lexically ordered")
	}
	for _, file := range files {
		contents, err := migrationFS.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if count := strings.Count(strings.ToUpper(string(contents)), "CREATE TABLE"); count == 0 {
			continue // extensions, seed data, and forward-compatible ALTER migrations
		} else if count != 1 {
			t.Fatalf("%s must create exactly one table, got %d", file, count)
		}
	}
}
