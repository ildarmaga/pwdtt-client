package backend

import (
	"encoding/base64"
	"testing"
)

func TestIsPanelSubURL(t *testing.T) {
	ok := []string{
		"https://dev-gmod.mooo.com:2096/subs/lmz1szh6djigb814",
		"https://devgamemaga.mooo.com:2096/sub/abc12345abcdef01",
		"http://127.0.0.1:2096/subs/testtoken123456",
	}
	for _, u := range ok {
		if !IsPanelSubURL(u) {
			t.Fatalf("expected ok: %s", u)
		}
	}
	bad := []string{
		"",
		"wdtt://eyJpcCI6IjEuMi4zLjQifQ==",
		"https://example.com/vpn/config",
		"https://evil.com:2096/other/token12345678",
		"ftp://dev-gmod.mooo.com:2096/subs/lmz1szh6djigb814",
	}
	for _, u := range bad {
		if IsPanelSubURL(u) {
			t.Fatalf("expected bad: %s", u)
		}
	}
}

func TestParseJSONWdttLinkDidAndVkHash(t *testing.T) {
	payload := `{"vpn":"MAGIC","name":"PC","ip":"dev-gmod.mooo.com","dtls":56000,"pass":"secret","did":"device-uuid-1","vk_hash":"hash1,hash2"}`
	link := "wdtt://" + base64.StdEncoding.EncodeToString([]byte(payload))
	p, err := parseWdttLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if p.DeviceID != "device-uuid-1" {
		t.Fatalf("did: %+v", p)
	}
	if len(p.Hashes) != 2 || p.Hashes[0] != "hash1" {
		t.Fatalf("hashes: %+v", p.Hashes)
	}
}
