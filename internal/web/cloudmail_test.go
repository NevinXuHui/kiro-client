package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleCloudMailListReturnsArray(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/cloudmail/list", nil)
	rec := httptest.NewRecorder()
	s.HandleCloudMailList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var configs []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &configs); err != nil {
		t.Fatalf("expected JSON array, got %s: %v", rec.Body.String(), err)
	}
}

func TestHandleCloudMailTestRejectsInvalidJSON(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/cloudmail/test", strings.NewReader(`{`))
	rec := httptest.NewRecorder()
	s.HandleCloudMailTest(rec, req)
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["error"] == nil {
		t.Fatalf("expected error, got %v", m)
	}
}
