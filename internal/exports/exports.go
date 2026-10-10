// Package exports holds the exports the server has created: snapshots of a
// dataset at a position, with the coverage and the file digest that their
// manifests report. It knows nothing of HTTP: the API reads the clock and
// checks access, and the store records exports and regenerates their files
// from the snapshots its SnapshotFunc returns.
//
// An export's file is never stored. History does not change, so the
// revisions at or below an export's position, and so the canonical bytes of
// its file, are the same whenever they are generated.
package exports

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/nslaughter/financial-data-api/internal/fixtures"
	"github.com/nslaughter/financial-data-api/internal/history"
)

const (
	// Lifetime is how long an export is kept, measured from its created_at.
	// It expires at that instant.
	Lifetime = 86400 * time.Second
	// FileName is the name of an export's one file.
	FileName = "revisions.jsonl"
	// idPrefix begins every export identifier.
	idPrefix = "exp_"
	// idBytes is how many random bytes an identifier carries: 64 bits, the
	// least spec/api.md allows.
	idBytes = 8
)

// Export is an export as its manifest describes it. It does not change after
// the store creates it.
type Export struct {
	ID        string
	DatasetID string
	// Position is the dataset's head position when the export was created.
	// The snapshot is every revision of the dataset at or below it.
	Position  int64
	CreatedAt time.Time
	Coverage  Coverage
	File      File
}

// ExpiresAt is when the export expires: Lifetime after its creation.
func (e Export) ExpiresAt() time.Time { return e.CreatedAt.Add(Lifetime) }

// Expired reports whether the export has expired at now: it is available
// while now is before ExpiresAt.
func (e Export) Expired(now time.Time) bool { return !now.Before(e.ExpiresAt()) }

// Coverage counts what an export's file contains.
type Coverage struct {
	// SeriesIDs are the series with at least one revision in the file,
	// ascending. Never nil.
	SeriesIDs        []string
	ObservationCount int
	RevisionCount    int
	// PeriodStart is the earliest period_start in the file, and PeriodEnd
	// the latest period_end. Both are nil for an empty file.
	PeriodStart, PeriodEnd *string
}

// File describes an export's file.
type File struct {
	RecordCount int
	SizeBytes   int
	// SHA256 is the file's SHA-256 digest in lowercase hexadecimal.
	SHA256 string
}

// SnapshotFunc returns a dataset's revisions with sequence at or below
// position, in sequence order, as history.History.Snapshot does. position is
// at or below the dataset's head position. It returns the same revisions
// whenever it is called with the same arguments, since the store regenerates
// an export's file from them.
type SnapshotFunc func(datasetID string, position int64) []fixtures.Revision

// Store holds exports. It is safe for concurrent use.
type Store struct {
	snapshot SnapshotFunc
	ids      *issuer

	mu      sync.Mutex
	exports map[string]Export // by ID
}

// issuer issues export identifiers and remembers every one it has issued, so
// that none is issued twice, even by stores that replace a cleared one.
type issuer struct {
	random io.Reader // crypto/rand.Reader, except in tests

	mu     sync.Mutex
	issued map[string]bool
}

// NewStore returns an empty store of exports of the datasets whose snapshots
// snapshot returns.
func NewStore(snapshot SnapshotFunc) *Store {
	return newStore(snapshot, &issuer{random: rand.Reader, issued: map[string]bool{}})
}

func newStore(snapshot SnapshotFunc, ids *issuer) *Store {
	return &Store{snapshot: snapshot, ids: ids, exports: map[string]Export{}}
}

// Cleared returns a new, empty store that shares s's record of issued
// identifiers, so that it never issues an identifier that s, or any store
// before it, issued. s is unchanged.
func (s *Store) Cleared() *Store {
	return newStore(s.snapshot, s.ids)
}

// Create creates an export of the dataset at position, which is at or below
// the dataset's head position at createdAt, with a new identifier.
func (s *Store) Create(datasetID string, position int64, createdAt time.Time) (Export, error) {
	id, err := s.ids.issue()
	if err != nil {
		return Export{}, err
	}
	revisions := s.snapshot(datasetID, position)
	data := history.ExportFile(revisions)
	digest := sha256.Sum256(data)
	e := Export{
		ID:        id,
		DatasetID: datasetID,
		Position:  position,
		CreatedAt: createdAt,
		Coverage:  Coverage{SeriesIDs: []string{}, RevisionCount: len(revisions)},
		File: File{
			RecordCount: len(revisions),
			SizeBytes:   len(data),
			SHA256:      hex.EncodeToString(digest[:]),
		},
	}
	observations := map[string]bool{}
	for _, r := range revisions {
		if !slices.Contains(e.Coverage.SeriesIDs, r.SeriesID) {
			e.Coverage.SeriesIDs = append(e.Coverage.SeriesIDs, r.SeriesID)
		}
		observations[r.ObservationID] = true
		// Dates in the form YYYY-MM-DD order as strings do.
		if e.Coverage.PeriodStart == nil || r.PeriodStart < *e.Coverage.PeriodStart {
			e.Coverage.PeriodStart = &r.PeriodStart
		}
		if e.Coverage.PeriodEnd == nil || r.PeriodEnd > *e.Coverage.PeriodEnd {
			e.Coverage.PeriodEnd = &r.PeriodEnd
		}
	}
	slices.Sort(e.Coverage.SeriesIDs)
	e.Coverage.ObservationCount = len(observations)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.exports[id] = e
	return e, nil
}

// Get returns the export with the identifier id, and whether there is one.
func (s *Store) Get(id string) (Export, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.exports[id]
	return e, ok
}

// FileBytes returns the canonical bytes of e's file: every revision of its
// dataset at or below its position, in sequence order. They are the bytes
// whose length and digest e.File reports.
func (s *Store) FileBytes(e Export) []byte {
	return history.ExportFile(s.snapshot(e.DatasetID, e.Position))
}

// issue returns a new identifier: idPrefix and idBytes from the random
// source in lowercase hexadecimal, never one issued before.
func (i *issuer) issue() (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	b := make([]byte, idBytes)
	for {
		if _, err := io.ReadFull(i.random, b); err != nil {
			return "", fmt.Errorf("exports: generating an identifier: %w", err)
		}
		id := idPrefix + hex.EncodeToString(b)
		if !i.issued[id] {
			i.issued[id] = true
			return id, nil
		}
	}
}
