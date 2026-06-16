package notify_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/notify"
)

// A 2xx response is success; the poster sends the JSON body to the URL.
func TestHTTPWebhookPosterSuccess(t *testing.T) {
	var gotBody []byte
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	err := notify.NewHTTPWebhookPoster(srv.Client()).Post(context.Background(), srv.URL, []byte(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if string(gotBody) != `{"text":"hi"}` {
		t.Errorf("body = %q", gotBody)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q", gotCT)
	}
}

// A non-2xx response is an error so the reconciler retries the tick.
func TestHTTPWebhookPosterNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := notify.NewHTTPWebhookPoster(srv.Client()).Post(context.Background(), srv.URL, []byte(`{}`))
	if err == nil {
		t.Fatal("expected an error on a 500 response")
	}
}
