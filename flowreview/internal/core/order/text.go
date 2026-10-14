package order

import (
	"fmt"
	"io"
)

// Text writes o as one screen of plain text: each flow group with its title
// and files in call order, each file's Why indented beneath it, then any
// unclassified files. This is `flowreview order`'s default output (FR-24
// mounts the --json alternative).
func Text(o Order, w io.Writer) {
	fmt.Fprintf(w, "order: %s", o.Kind)
	switch len(o.Groups) {
	case 0:
	case 1:
		fmt.Fprint(w, " (1 group)")
	default:
		fmt.Fprintf(w, " (%d groups)", len(o.Groups))
	}
	fmt.Fprintln(w)

	for _, g := range o.Groups {
		fmt.Fprintf(w, "\n== %s ==\n", g.Title)
		for _, f := range g.Files {
			fmt.Fprintf(w, "  %s\n", f.Path)
			if f.Why != "" {
				fmt.Fprintf(w, "      %s\n", f.Why)
			}
		}
	}

	if len(o.Unclassified) > 0 {
		fmt.Fprintf(w, "\nUnclassified (%d):\n", len(o.Unclassified))
		for _, f := range o.Unclassified {
			fmt.Fprintf(w, "  %s\n", f.Path)
		}
	}
}
