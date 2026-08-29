package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleDataDirGet(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/config/data-dir", nil)
	rec := httptest.NewRecorder()
	s.HandleDataDir(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["path"] == nil || m["path"] == "" {
		t.Fatalf("expected path, got %v", m)
	}
}

func TestHandleOutputAccountsReturnsSuccess(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/accounts/output", nil)
	rec := httptest.NewRecorder()
	s.HandleOutputAccounts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["accounts"]; !ok {
		t.Fatalf("missing accounts: %v", m)
	}
}

func TestHandleManualRegisterStatusIdle(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/register/manual/status", nil)
	rec := httptest.NewRecorder()
	s.HandleManualRegisterStatus(rec, req)
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["running"] != false {
		t.Fatalf("expected running=false, got %v", m)
	}
}
