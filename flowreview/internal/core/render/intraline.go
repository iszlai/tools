package render

import "unicode"

// maxIntralineTokens caps the word-tokens-per-side the LCS diff below will
// run on. Its cost is O(n*m); a line past this (minified JS, a generated
// one-liner) just renders without intra-line marks rather than risking a
// pathological hunk stalling a render.
const maxIntralineTokens = 400

// diffRanges finds the byte ranges of oldText and newText that an LCS over
// word tokens says changed, for the <mark> wrapping in lineHTML. It returns
// nil, nil when there is nothing to mark (identical text, or either side too
// large -- see maxIntralineTokens).
func diffRanges(oldText, newText string) (oldRanges, newRanges []byteRange) {
	oldToks := wordTokens(oldText)
	newToks := wordTokens(newText)
	if len(oldToks) == 0 || len(newToks) == 0 {
		return nil, nil
	}
	if len(oldToks) > maxIntralineTokens || len(newToks) > maxIntralineTokens {
		return nil, nil
	}

	oldMatched, newMatched := lcsMatch(oldToks, newToks)
	return unmatchedRanges(oldMatched, tokenByteRanges(oldToks)), unmatchedRanges(newMatched, tokenByteRanges(newToks))
}

// wordTokens splits s into tokens for intra-line diffing: a maximal run of
// identifier runes, a maximal run of horizontal whitespace, or one other
// rune (punctuation, symbols) per token -- so a diff lands on "foo"/"bar" or
// "(" rather than splitting mid-identifier or merging unrelated punctuation.
func wordTokens(s string) []string {
	var out []string
	runes := []rune(s)
	for i := 0; i < len(runes); {
		j := i + 1
		switch {
		case isWordRune(runes[i]):
			for j < len(runes) && isWordRune(runes[j]) {
				j++
			}
		case runes[i] == ' ' || runes[i] == '\t':
			for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t') {
				j++
			}
		}
		out = append(out, string(runes[i:j]))
		i = j
	}
	return out
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// tokenByteRanges returns each token's byte span in the string wordTokens
// split it from, in order.
func tokenByteRanges(tokens []string) []byteRange {
	ranges := make([]byteRange, len(tokens))
	pos := 0
	for i, t := range tokens {
		ranges[i] = byteRange{pos, pos + len(t)}
		pos += len(t)
	}
	return ranges
}

// lcsMatch finds a longest common subsequence of old and new (by equal
// tokens) and reports which indices on each side belong to it.
func lcsMatch(old, new_ []string) (oldMatched, newMatched []bool) {
	n, m := len(old), len(new_)
	// dp[i][j] = LCS length of old[i:], new[j:].
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case old[i] == new_[j]:
				dp[i][j] = dp[i+1][j+1] + 1
			case dp[i+1][j] >= dp[i][j+1]:
				dp[i][j] = dp[i+1][j]
			default:
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	oldMatched = make([]bool, n)
	newMatched = make([]bool, m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case old[i] == new_[j]:
			oldMatched[i] = true
			newMatched[j] = true
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			i++
		default:
			j++
		}
	}
	return oldMatched, newMatched
}

// unmatchedRanges merges the byte ranges of consecutive unmatched tokens
// into one range each, so adjacent changed tokens ("line", " ", "TWO") mark
// as a single <mark> rather than three abutting ones.
func unmatchedRanges(matched []bool, ranges []byteRange) []byteRange {
	var out []byteRange
	for i := 0; i < len(matched); {
		if matched[i] {
			i++
			continue
		}
		j := i
		for j < len(matched) && !matched[j] {
			j++
		}
		out = append(out, byteRange{ranges[i].Start, ranges[j-1].End})
		i = j
	}
	return out
}
