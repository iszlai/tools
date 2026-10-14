package gherkin

import (
	"regexp"
	"strings"
)

var cucumberParamRe = regexp.MustCompile(`\{(int|float|string|word)\}`)

// cucumberExpressionToRegex translates a Cucumber expression ("a user named
// {string}") into an anchored Go regexp. Only the four parameter types this
// slice targets are supported: {int}, {float}, {string}, {word}; anything
// else is literal text. {string} does not include the surrounding quotes in
// the expression text itself (Cucumber's own convention), so the generated
// pattern supplies them.
func cucumberExpressionToRegex(expr string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteByte('^')
	last := 0
	for _, loc := range cucumberParamRe.FindAllStringIndex(expr, -1) {
		b.WriteString(regexp.QuoteMeta(expr[last:loc[0]]))
		switch expr[loc[0]+1 : loc[1]-1] {
		case "int":
			b.WriteString(`(-?\d+)`)
		case "float":
			b.WriteString(`(-?\d+(?:\.\d+)?)`)
		case "string":
			b.WriteString(`"([^"]*)"`)
		case "word":
			b.WriteString(`(\S+)`)
		}
		last = loc[1]
	}
	b.WriteString(regexp.QuoteMeta(expr[last:]))
	b.WriteByte('$')
	return regexp.Compile(b.String())
}
