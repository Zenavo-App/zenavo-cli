package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeError(t *testing.T) {
	cases := map[string]string{
		`{"detail":"Project changed"}`: "Project changed",
		`{"detail":[{"loc":["body","manifest","name"],"msg":"Field required"},{"loc":["body"],"msg":"Bad"}]}`: "manifest.name: Field required; Bad",
		`{"detail":["Domain taken","Port busy"]}`:                                                             "Domain taken; Port busy",
		`<html>`: "Bad Gateway",
	}
	for body, want := range cases {
		err := decodeError(502, []byte(body))
		if err.Error() != want {
			t.Errorf("decodeError(%s) = %q, want %q", body, err, want)
		}
	}
}

func TestNoRedirectsAndBearer(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization") != ""
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/me" {
			if r.Header.Get("Authorization") != "Bearer zv_pat_t" {
				t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
			}
			http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "zv_pat_t", "test")
	if err := c.Get(context.Background(), "/auth/me", nil); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("err = %v, want a redirect error", err)
	}
	if leaked {
		t.Fatal("token reached the redirect target")
	}
}

func TestStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("retry: 3000\n\n: ping\n\ndata: \"one\"\n\ndata: \"two\"\n\nevent: end\ndata: {}\n\ndata: \"after\"\n\n"))
	}))
	defer srv.Close()
	var got []string
	err := New(srv.URL, "t", "test").Stream(context.Background(), "/x", func(d string) error {
		got = append(got, d)
		return nil
	})
	if err != nil || strings.Join(got, ",") != `"one","two"` {
		t.Fatalf("got %v, %v", got, err)
	}
}
