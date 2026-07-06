//go:build !windows

package backend

import (
	"net/http"
	"time"
)

func withUpdateDirectEgress(...string) func() { return func() {} }

func newUpdateHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}
