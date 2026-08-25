package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlePoolsListReturnsArray(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/pools/list", nil)
	rec := httptest.NewRecorder()
	s.HandlePoolsList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var pools []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &pools); err != nil {
		t.Fatalf("expected JSON array, got %s: %v", rec.Body.String(), err)
	}
}

func TestHandlePoolsRefreshRequiresID(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/pools/refresh", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	s.HandlePoolsRefresh(rec, req)
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["error"] == nil {
		t.Fatalf("expected error, got %v", m)
	}
}

func TestHandlePoolGetRequiresID(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/pools/get", nil)
	rec := httptest.NewRecorder()
	s.HandlePoolGet(rec, req)
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["error"] == nil {
		t.Fatalf("expected error, got %v", m)
	}
}

func TestHandlePoolRefreshAccountRequiresFields(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/pools/refresh-account", strings.NewReader(`{"poolID":"x"}`))
	rec := httptest.NewRecorder()
	s.HandlePoolRefreshAccount(rec, req)
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["error"] == nil {
		t.Fatalf("expected error, got %v", m)
	}
}

func TestHandlePoolExportAccountsRequiresID(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/pools/export-accounts", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	s.HandlePoolExportAccounts(rec, req)
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["error"] == nil {
		t.Fatalf("expected error, got %v", m)
	}
}

func TestHandlePoolUpdateRequiresID(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/pools/update", strings.NewReader(`{"name":"x"}`))
	rec := httptest.NewRecorder()
	s.HandlePoolUpdate(rec, req)
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["error"] == nil {
		t.Fatalf("expected error, got %v", m)
	}
}
