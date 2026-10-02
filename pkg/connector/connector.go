// Package connector defines the experimental connector contract for Findrail.
// This API may change before v1.0. Connectors enumerate documents; indexing,
// search, presentation, and storage remain responsibilities of the host.
package connector

import (
	"context"
	"time"
)

// Source identifies one explicitly configured collection of documents.
type Source struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	Root string `json:"root"`
}

// Document is an extracted, UTF-8 document with a stable identity.
type Document struct {
	ID         string
	SourceID   string
	Title      string
	URI        string
	Path       string
	Content    string
	Hash       string
	SizeBytes  int64
	ModifiedAt time.Time
}

// Connector performs a full inventory of one source. A successful scan means
// the enumeration is complete. An incomplete scan must return an error so the
// host can avoid removing documents that were merely inaccessible.
type Connector interface {
	Source() Source
	Scan(context.Context, func(Document) error) (Report, error)
}

// Report describes scan work without logging document contents.
type Report struct {
	Seen    int `json:"seen"`
	Skipped int `json:"skipped"`
}
