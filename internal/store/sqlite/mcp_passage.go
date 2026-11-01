package sqlite

import (
	"context"

	"github.com/Lephiziel/findrail/internal/search"
)

// PassageForSource is the MCP's source-scoped bridge to the existing guarded
// passage reader. EvidenceForSource first verifies provenance, then Passage
// rechecks the expected snapshot identity in its read transaction.
func (s *Store) PassageForSource(ctx context.Context, sourceID, id, query, expected string, start int) (search.Evidence, error) {
	if sourceID == "" {
		return search.Evidence{}, search.ErrNotFound
	}
	verified, err := s.EvidenceForSource(ctx, sourceID, id, 0)
	if err != nil {
		return search.Evidence{}, err
	}
	if verified.OriginalIdentity != expected {
		return search.Evidence{}, search.ErrNotFound
	}
	return s.Passage(ctx, id, query, expected, start)
}
