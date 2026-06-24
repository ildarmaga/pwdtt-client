package core

import (
	"fmt"
	"testing"
)

func TestVKParseCookiesPayload(t *testing.T) {
	header, err := vkParseCookiesPayload([]byte(`[{"name":"remixsid","value":"abc"},{"name":"remixlang","value":"0"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if header != "remixsid=abc; remixlang=0" {
		t.Fatalf("got %q", header)
	}
	header, err = vkParseCookiesPayload([]byte("remixsid=xyz; remixlang=0"))
	if err != nil || header != "remixsid=xyz; remixlang=0" {
		t.Fatalf("string form: %v %q", err, header)
	}
	if _, err := vkParseCookiesPayload([]byte(`[{"name":"remixlang","value":"0"}]`)); err == nil {
		t.Fatal("expected missing remixsid error")
	}
}

func TestVKCookiesLiveValidExpired(t *testing.T) {
	oldValidate := vkCookieValidateLive
	vkCookieValidateLive = func(string) error { return fmt.Errorf("empty access_token") }
	defer func() {
		vkCookieValidateLive = oldValidate
		invalidateVKCookieStatusCache()
	}()
	invalidateVKCookieStatusCache()
	if err := vkCookiesLiveValid("remixsid=expired"); err == nil {
		t.Fatal("expected expired error")
	}
}
