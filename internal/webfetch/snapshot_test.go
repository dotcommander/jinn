package webfetch

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func (st *snapshotStore) listIDs() ([]string, error) {
	entries, err := os.ReadDir(st.dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), snapshotFileSuffix) {
			ids = append(ids, strings.TrimSuffix(entry.Name(), snapshotFileSuffix))
		}
	}
	return ids, nil
}

func snapshotTestService(t *testing.T, ttl time.Duration) *Service {
	t.Helper()
	return &Service{snapshots: newSnapshotStore(t.TempDir(), ttl)}
}

func TestSnapshotSearchRoundTripAcrossServices(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writer := &Service{snapshots: newSnapshotStore(dir, time.Hour)}
	results := []SearchResult{{Title: "Hit", URL: "https://example.com/a", Description: "first"}}
	info := writer.SaveSearchSnapshot(SearchResponse{Query: "go", Provider: providerExa, Results: results})
	if info.Status != "stored" || info.ID == "" || info.Digest == "" {
		t.Fatalf("info = %+v, want stored with id+digest", info)
	}
	// A second service over the same directory reads the identical content
	// without any network work (acceptance: process A → process B).
	reader := &Service{snapshots: newSnapshotStore(dir, time.Hour)}
	snap, err := reader.GetSnapshot(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Kind != snapshotKindSearch || snap.Query != "go" || len(snap.Results) != 1 || snap.Results[0].URL != "https://example.com/a" {
		t.Fatalf("snap = %+v", snap)
	}
}

func TestSnapshotDocumentRoundTrip(t *testing.T) {
	t.Parallel()
	service := snapshotTestService(t, time.Hour)
	doc := Document{URL: "https://example.com/", FinalURL: "https://example.com/", Title: "T", Content: "# T\nbody\n"}
	info := service.SaveDocumentSnapshot(doc)
	if info.Status != "stored" {
		t.Fatalf("info = %+v", info)
	}
	snap, err := service.GetSnapshot(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Kind != snapshotKindDocument || snap.Document == nil || snap.Document.Content != "# T\nbody\n" {
		t.Fatalf("snap = %+v", snap)
	}
}

func TestSnapshotErrorsAreDistinct(t *testing.T) {
	t.Parallel()
	service := snapshotTestService(t, time.Hour)
	// Unknown id.
	_, err := service.GetSnapshot("0123456789abcdef0123456789abcdef")
	var coded *CodedError
	if !errors.As(err, &coded) || coded.Code != ErrorCodeSnapshotNotFound {
		t.Fatalf("unknown id error = %#v, want snapshot_not_found", err)
	}
	// Malformed id.
	_, err = service.GetSnapshot("not-an-id")
	if !errors.As(err, &coded) || coded.Code != ErrorCodeSnapshotInvalid {
		t.Fatalf("malformed id error = %#v, want snapshot_invalid", err)
	}
	// Expired snapshot (short TTL + backdated file).
	expired := snapshotTestService(t, time.Nanosecond)
	info := expired.SaveDocumentSnapshot(Document{Content: "x"})
	if info.Status == "stored" {
		time.Sleep(2 * time.Millisecond)
		_, err = expired.GetSnapshot(info.ID)
		if !errors.As(err, &coded) || coded.Code != ErrorCodeSnapshotExpired {
			t.Fatalf("expired error = %#v, want snapshot_expired", err)
		}
	}
}

func TestSnapshotPersistenceFailureIsUnavailable(t *testing.T) {
	t.Parallel()
	// nil store → unavailable, never an error for the retrieval path.
	service := &Service{}
	info := service.SaveSearchSnapshot(SearchResponse{Query: "go"})
	if info.Status != "unavailable" {
		t.Fatalf("info = %+v, want unavailable", info)
	}
}

func TestSnapshotEvictionBoundsEntries(t *testing.T) {
	t.Parallel()
	service := snapshotTestService(t, time.Hour)
	service.snapshots.maxEntries = 2
	for i := 0; i < 4; i++ {
		service.SaveDocumentSnapshot(Document{Content: "body"})
	}
	entries, err := service.snapshots.listIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > 2 {
		t.Fatalf("entries = %d, want <= 2", len(entries))
	}
}
