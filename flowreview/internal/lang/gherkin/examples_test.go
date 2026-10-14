package gherkin

import "testing"

func TestParseExamplesTable(t *testing.T) {
	lines := []string{
		"  Scenario Outline: Say hello",            // 1
		"    Given a user named \"<name>\" exists", // 2
		"",                          // 3
		"    Examples:",             // 4
		"      | name | greeting |", // 5
		"      | Bob  | hi Bob   |", // 6
		"      | Sue  | hi Sue   |", // 7
	}
	ex := parseExamplesTable(lines, 1, 7)
	if ex == nil {
		t.Fatal("parseExamplesTable returned nil")
	}
	if want := []string{"name", "greeting"}; !equalStrings(ex.header, want) {
		t.Errorf("header = %v, want %v", ex.header, want)
	}
	if len(ex.rows) != 2 {
		t.Fatalf("rows = %v, want 2 rows", ex.rows)
	}
	if want := []string{"Bob", "hi Bob"}; !equalStrings(ex.rows[0], want) {
		t.Errorf("rows[0] = %v, want %v", ex.rows[0], want)
	}
}

func TestParseExamplesTable_NoneFound(t *testing.T) {
	lines := []string{"Scenario: no outline here", "Given a thing happens"}
	if ex := parseExamplesTable(lines, 1, 2); ex != nil {
		t.Errorf("parseExamplesTable = %+v, want nil", ex)
	}
}

func TestSubstituteExample(t *testing.T) {
	ex := &exampleTable{
		header: []string{"name", "greeting"},
		rows:   [][]string{{"Bob", "hi Bob"}},
	}
	got, ok := substituteExample(`a user named "<name>" exists`, ex)
	if !ok {
		t.Fatal("substituteExample reported no match")
	}
	if want := `a user named "Bob" exists`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	if _, ok := substituteExample("a user with <unknown> column", ex); ok {
		t.Error("substituteExample should fail when a placeholder has no matching column")
	}
}

func TestWildcardSubstitute(t *testing.T) {
	got := wildcardSubstitute(`a user named "<name>" exists`)
	if want := `a user named "x" exists`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
