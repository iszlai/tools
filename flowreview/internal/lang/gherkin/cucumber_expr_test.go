package gherkin

import "testing"

func TestCucumberExpressionToRegex(t *testing.T) {
	cases := []struct {
		name    string
		expr    string
		matches []string
		misses  []string
	}{
		{
			name:    "int",
			expr:    "there are {int} cucumbers",
			matches: []string{"there are 3 cucumbers", "there are -1 cucumbers"},
			misses:  []string{"there are 3.5 cucumbers", "there are three cucumbers"},
		},
		{
			name:    "float",
			expr:    "the price is {float}",
			matches: []string{"the price is 3.50", "the price is 3"},
			misses:  []string{"the price is abc"},
		},
		{
			name:    "string",
			expr:    "a user named {string}",
			matches: []string{`a user named "Ann"`, `a user named ""`},
			misses:  []string{"a user named Ann"},
		},
		{
			name:    "word",
			expr:    "the status is {word}",
			matches: []string{"the status is pending"},
			misses:  []string{"the status is not pending"},
		},
		{
			name:    "literal only",
			expr:    "the system is ready",
			matches: []string{"the system is ready"},
			misses:  []string{"the system is ready!"},
		},
		{
			name:    "regex metachars in literal text are escaped",
			expr:    "a (parenthesized) value of {int}",
			matches: []string{"a (parenthesized) value of 5"},
			misses:  []string{"a xparenthesizedx value of 5"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			re, err := cucumberExpressionToRegex(c.expr)
			if err != nil {
				t.Fatalf("cucumberExpressionToRegex(%q): %v", c.expr, err)
			}
			for _, m := range c.matches {
				if !re.MatchString(m) {
					t.Errorf("pattern %q should match %q", re, m)
				}
			}
			for _, m := range c.misses {
				if re.MatchString(m) {
					t.Errorf("pattern %q should not match %q", re, m)
				}
			}
		})
	}
}
