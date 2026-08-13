package wbstream

import (
	"testing"
)

func TestValidateCookiesJSON(t *testing.T) {
	raw := []byte(`[
		{"name":"__wb_device_id","value":"device-1"},
		{"name":"wbx-refresh","value":"refresh-token"}
	]`)
	if err := ValidateCookiesJSON(raw); err != nil {
		t.Fatal(err)
	}
}

func TestParseRoomID(t *testing.T) {
	cases := map[string]string{
		"wbstream://abc12345":                      "abc12345",
		"https://stream.wb.ru/room/xyz98765":       "xyz98765",
		"  bare-room-id  ":                        "bare-room-id",
	}
	for in, want := range cases {
		if got := ParseRoomID(in); got != want {
			t.Fatalf("%q => %q, want %q", in, got, want)
		}
	}
}
