//go:build !windows

package backend

import (
	"net/http"
	"time"
)

func withUpdateDirectEgress(...string) func() { return func() {} }

func newUpdateHTTPClient(timeout time.Duration, _ bool) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: nil, // ignore HTTP_PROXY — updates go direct
		},
	}
}
