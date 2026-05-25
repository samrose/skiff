package registry

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPoll_EmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		// Empty results, last_seq equals since.
		w.Write([]byte(`{"results":[],"last_seq":42}`))
	}))
	defer srv.Close()

	var rows []ChangeRow
	lastSeq, err := Poll(t.Context(), srv.Client(), srv.URL, "test-ua/1", 42, 100, func(row ChangeRow) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
	if lastSeq != 42 {
		t.Errorf("expected lastSeq=42, got %d", lastSeq)
	}
}

func TestPoll_ThreeRows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{
			"results": [
				{"seq":101,"id":"foo"},
				{"seq":102,"id":"bar"},
				{"seq":103,"id":"baz"}
			],
			"last_seq": 103
		}`))
	}))
	defer srv.Close()

	var rows []ChangeRow
	lastSeq, err := Poll(t.Context(), srv.Client(), srv.URL, "test-ua/1", 100, 100, func(row ChangeRow) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].ID != "foo" || rows[0].Seq != 101 {
		t.Errorf("row[0] = %+v, want {Seq:101 ID:foo}", rows[0])
	}
	if rows[1].ID != "bar" || rows[1].Seq != 102 {
		t.Errorf("row[1] = %+v, want {Seq:102 ID:bar}", rows[1])
	}
	if rows[2].ID != "baz" || rows[2].Seq != 103 {
		t.Errorf("row[2] = %+v, want {Seq:103 ID:baz}", rows[2])
	}
	if lastSeq != 103 {
		t.Errorf("expected lastSeq=103, got %d", lastSeq)
	}
}

func TestPoll_CallbackError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{
			"results": [
				{"seq":201,"id":"alpha"},
				{"seq":202,"id":"beta"},
				{"seq":203,"id":"gamma"}
			],
			"last_seq": 203
		}`))
	}))
	defer srv.Close()

	sentinelErr := errors.New("stop processing")
	var rows []ChangeRow
	lastSeq, err := Poll(t.Context(), srv.Client(), srv.URL, "test-ua/1", 200, 100, func(row ChangeRow) error {
		rows = append(rows, row)
		if row.ID == "beta" {
			return sentinelErr
		}
		return nil
	})
	if !errors.Is(err, sentinelErr) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	// Should have received alpha + beta (and stopped at beta's error).
	if len(rows) != 2 {
		t.Errorf("expected 2 rows before error, got %d", len(rows))
	}
	// lastSeq should be 202 (the seq of the failing row).
	if lastSeq != 202 {
		t.Errorf("expected lastSeq=202 at failure point, got %d", lastSeq)
	}
}

func TestPoll_Server503(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	lastSeq, err := Poll(t.Context(), srv.Client(), srv.URL, "test-ua/1", 0, 100, func(row ChangeRow) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for 503, got nil")
	}
	// Should contain the status code.
	if lastSeq != 0 {
		t.Errorf("expected lastSeq unchanged (0), got %d", lastSeq)
	}
}

func TestPoll_UserAgentHeader(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"results":[],"last_seq":0}`))
	}))
	defer srv.Close()

	Poll(t.Context(), srv.Client(), srv.URL, "my-test-agent/2", 0, 100, func(row ChangeRow) error { //nolint:errcheck
		return nil
	})
	if gotUA != "my-test-agent/2" {
		t.Errorf("expected User-Agent 'my-test-agent/2', got %q", gotUA)
	}
}
