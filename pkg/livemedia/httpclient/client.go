package httpclient

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	MaxJSONResponseBytes    int64 = 64 << 20
	MaxCatalogResponseBytes int64 = 256 << 20
	DefaultTimeout                = 30 * time.Second
)

func New() *http.Client {
	return &http.Client{Timeout: DefaultTimeout}
}

func RedactErrorURL(err error) error {
	if err == nil {
		return nil
	}
	var urlError *url.Error
	if errors.As(err, &urlError) {
		if urlError.Err != nil && !errorTextMayContainURL(urlError.Err.Error()) {
			return fmt.Errorf("%s request failed: %w", urlError.Op, urlError.Err)
		}
		return &redactedRequestError{op: urlError.Op, cause: urlError.Err}
	}
	return err
}

func errorTextMayContainURL(message string) bool {
	message = strings.ToLower(message)
	for _, marker := range []string{"://", "//", "@", "username=", "password=", "token=", "api_key=", "apikey="} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

type redactedRequestError struct {
	op    string
	cause error
}

func (e *redactedRequestError) Error() string {
	if e.op == "" {
		return "request failed"
	}
	return e.op + " request failed"
}

func (e *redactedRequestError) Unwrap() error {
	return e.cause
}

func ReadAllLimit(reader io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("response size limit must be positive")
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d byte limit", limit)
	}
	return data, nil
}
