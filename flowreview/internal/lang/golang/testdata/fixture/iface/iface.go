// Package iface exercises FR-14's symbol construction for an interface that
// embeds another interface.
package iface

// Reader can read.
type Reader interface {
	Read() string
}

// ReadCloser embeds Reader and adds Close. Its Signature should carry both
// methods: the embedded one is promoted into the interface's method set.
type ReadCloser interface {
	Reader
	Close() error
}
