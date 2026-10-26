package httpclient

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewAllowsSlowRemoteCatalogResponses(t *testing.T) {
	t.Parallel()

	if timeout := New().Timeout; timeout != 30*time.Second {
		t.Fatalf("HTTP client timeout = %s, want 30s", timeout)
	}
}

func TestRedactErrorURLPreservesCauseWithoutCredentials(t *testing.T) {
	t.Parallel()

	cause := errors.New("dial failed")
	err := RedactErrorURL(&url.Error{Op: "Get", URL: "https://provider.example/player?username=demo&password=secret", Err: cause})
	if !errors.Is(err, cause) {
		t.Fatalf("expected wrapped transport cause, got %v", err)
	}
	if !strings.Contains(err.Error(), "dial failed") {
		t.Fatalf("expected credential-free transport detail, got %v", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "provider.example") {
		t.Fatalf("transport error exposed request URL: %v", err)
	}
}

func TestRedactErrorURLHidesMalformedRedirectLocation(t *testing.T) {
	t.Parallel()

	const secret = "hunter2secret"
	for name, location := range map[string]string{
		"absolute":        "http://demo:" + secret + "@%zz/",
		"scheme-relative": "//demo:" + secret + "@%zz/",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", location)
				w.WriteHeader(http.StatusFound)
			}))
			defer server.Close()

			_, err := New().Get(server.URL)
			if err == nil {
				t.Fatal("expected malformed redirect to fail")
			}
			redacted := RedactErrorURL(err).Error()
			if strings.Contains(redacted, secret) || strings.Contains(redacted, "%zz") {
				t.Fatalf("redacted transport error exposed malformed redirect: %v", redacted)
			}
		})
	}
}

func TestReadAllLimitRejectsOversizedPayload(t *testing.T) {
	t.Parallel()

	if _, err := ReadAllLimit(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("expected oversized response to be rejected")
	}
}

func TestReadAllLimitReturnsPayloadWithinLimit(t *testing.T) {
	t.Parallel()

	data, err := ReadAllLimit(strings.NewReader("1234"), 4)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if string(data) != "1234" {
		t.Fatalf("unexpected payload %q", data)
	}
}
