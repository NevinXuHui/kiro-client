package email

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func writeCloudMailOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"code": 200, "message": "ok", "data": data,
	})
}

func writeCloudMailBiz401(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"code": 401, "message": "token验证失败", "data": nil,
	})
}

func TestUnauthorizedDetectsHTTPAndBusiness401(t *testing.T) {
	t.Parallel()
	if !unauthorized(401, []byte(`{}`)) {
		t.Fatal("http 401")
	}
	if !unauthorized(200, []byte(`{"code":401,"message":"token验证失败"}`)) {
		t.Fatal("json 401")
	}
	if unauthorized(200, []byte(`{"code":200,"message":"ok"}`)) {
		t.Fatal("json 200 should pass")
	}
}

func TestCloudMailSharedTokenOnce(t *testing.T) {
	resetCloudMailTokenCache()
	defer resetCloudMailTokenCache()

	var gens int32
	var mu sync.Mutex
	current := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/public/genToken":
			n := atomic.AddInt32(&gens, 1)
			mu.Lock()
			current = fmt.Sprintf("tok-%d", n)
			tok := current
			mu.Unlock()
			writeCloudMailOK(w, map[string]string{"token": tok})
		case "/api/public/emailList":
			mu.Lock()
			want := current
			mu.Unlock()
			if r.Header.Get("Authorization") != want {
				writeCloudMailBiz401(w)
				return
			}
			writeCloudMailOK(w, []interface{}{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := CloudMailConfig{URL: srv.URL, Email: "admin@x.com", Password: "p"}
	const n = 8
	errCh := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := NewCloudMailClient(cfg)
			_, err := c.EmailList("a@x.com", 1)
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("emailList: %v", err)
		}
	}
	if got := atomic.LoadInt32(&gens); got != 1 {
		t.Fatalf("genToken called %d times, want 1", got)
	}
}

func TestCloudMailRefreshOnBusiness401(t *testing.T) {
	resetCloudMailTokenCache()
	defer resetCloudMailTokenCache()

	var gens int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/public/genToken":
			atomic.AddInt32(&gens, 1)
			writeCloudMailOK(w, map[string]string{"token": "fresh"})
		case "/api/public/emailList":
			if r.Header.Get("Authorization") != "fresh" {
				writeCloudMailBiz401(w)
				return
			}
			writeCloudMailOK(w, []interface{}{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := CloudMailConfig{URL: srv.URL, Email: "admin@x.com", Password: "p"}
	c := NewCloudMailClient(cfg)
	c.slot.mu.Lock()
	c.slot.token = "stale"
	c.slot.mu.Unlock()

	if _, err := c.EmailList("a@x.com", 1); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&gens); got != 1 {
		t.Fatalf("genToken called %d times, want 1", got)
	}
	if c.currentToken() != "fresh" {
		t.Fatalf("token=%q", c.currentToken())
	}
}
