package radius

import (
	"fmt"
	"io"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// Text writes r as a one-screen plain-text summary: one section per changed
// function, method or interface method. This is FR-24's eventual
// `flowreview radius` output; FR-24 owns cmd/, so Text is exposed for it to
// call later and is not wired up to any command here.
func Text(r Report, w io.Writer) {
	if len(r.Functions) == 0 {
		fmt.Fprintln(w, "no changed functions, methods or interface methods in this diff")
		return
	}
	for _, fr := range r.Functions {
		fmt.Fprintf(w, "%s (%s)", fr.Symbol, fr.Kind)
		if fr.SignatureChanged {
			fmt.Fprint(w, " [signature changed]")
		}
		fmt.Fprintln(w)

		fmt.Fprintf(w, "  unchanged callers (%d):\n", len(fr.UnchangedCallers))
		for _, c := range fr.UnchangedCallers {
			fmt.Fprintf(w, "    %s%s%s\n", c.Symbol, siteSuffix(c.Site), inDiffSuffix(c.InDiff))
		}
		if len(fr.UnchangedCallers) == 0 {
			fmt.Fprintln(w, "    none")
		}

		if len(fr.CallSites) > 0 {
			fmt.Fprintf(w, "  call sites (%d):\n", len(fr.CallSites))
			for _, s := range fr.CallSites {
				fmt.Fprintf(w, "    %s%s%s\n", s.Caller, siteSuffix(s.Location), inDiffSuffix(s.InDiff))
			}
		}
		if len(fr.Implementers) > 0 {
			fmt.Fprintf(w, "  implementers (%d):\n", len(fr.Implementers))
			for _, i := range fr.Implementers {
				fmt.Fprintf(w, "    %s [%s]%s\n", i.Symbol, i.Confidence, inDiffSuffix(i.InDiff))
			}
		}
		for _, warn := range fr.Warnings {
			fmt.Fprintf(w, "  WARNING: %s\n", warn)
		}
		fmt.Fprintln(w)
	}
}

func siteSuffix(loc *model.Location) string {
	if loc == nil {
		return ""
	}
	return fmt.Sprintf(" (%s:%d:%d)", loc.Path, loc.Line, loc.Col)
}

func inDiffSuffix(inDiff bool) string {
	if inDiff {
		return " [in diff]"
	}
	return " [not in diff]"
}
