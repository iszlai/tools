package order

import (
	"path/filepath"
	"sort"
)

// Alphabetical orders every changed file by path, ascending. There is no
// flow narrative for this Kind, so every file lands in Order.Unclassified
// (Groups is always empty) -- same for Directory and DiffOrder below.
func Alphabetical(in Inputs) Order {
	entries := plainEntries(in)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return Order{Kind: KindAlpha, Unclassified: entries}
}

// Directory orders by directory, then by path within it -- a cheap way to
// read a diff file-tree-first instead of flow-first.
func Directory(in Inputs) Order {
	entries := plainEntries(in)
	sort.Slice(entries, func(i, j int) bool {
		di, dj := filepath.Dir(entries[i].Path), filepath.Dir(entries[j].Path)
		if di != dj {
			return di < dj
		}
		return entries[i].Path < entries[j].Path
	})
	return Order{Kind: KindDirectory, Unclassified: entries}
}

// DiffOrder is in.Files' own order, unchanged -- a neutral baseline to
// compare the other Kinds against.
func DiffOrder(in Inputs) Order {
	return Order{Kind: KindDiff, Unclassified: plainEntries(in)}
}

// Commits is FR-23's hook: ordering by the commits of the reviewed range, a
// hunk appearing under the first commit that introduced it. Not built in
// this slice.
func Commits(in Inputs) (Order, error) {
	return Order{}, ErrUnsupported
}

// plainEntries turns in.Files into FileEntry in their given order, with no
// Why/Symbols (those are Flow-specific) but Hotspot filled in when
// available.
func plainEntries(in Inputs) []FileEntry {
	out := make([]FileEntry, len(in.Files))
	for i, f := range in.Files {
		out[i] = FileEntry{FileID: f.ID, Path: f.Path(), Hotspot: hotspotOf(in, f.Path())}
	}
	return out
}
