package backend

import (
	"encoding/base64"
	"testing"
)

func TestIsPanelSubURL(t *testing.T) {
	ok := []string{
		"https://dev-gmod.mooo.com:2096/subs/lmz1szh6djigb814",
		"https://devgamemaga.mooo.com:2096/sub/abc12345abcdef01",
		"https://vpn.example.com/custom/path/217f4ls7t6rrwoy0",
		"http://127.0.0.1/subs/testtoken123456",
	}
	for _, u := range ok {
		if !IsPanelSubURL(u) {
			t.Fatalf("expected ok: %s", u)
		}
	}
	bad := []string{
		"",
		"wdtt://eyJpcCI6IjEuMi4zLjQifQ==",
		"https://example.com/",
		"ftp://dev-gmod.mooo.com:2096/subs/lmz1szh6djigb814",
	}
	for _, u := range bad {
		if IsPanelSubURL(u) {
			t.Fatalf("expected bad: %s", u)
		}
	}
}

func TestExtractSubURLFromWdttLink(t *testing.T) {
	payload := `{"vpn":"MAGIC VPN","name":"ildar","ip":"devgamemaga.mooo.com","dtls":56000,"pass":"secret","sub":"https://devgamemaga.mooo.com:2096/subs/217f4ls7t6rrwoy0"}`
	link := "wdtt://" + base64.StdEncoding.EncodeToString([]byte(payload))
	sub, err := ExtractSubURLFromWdttLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if sub != "https://devgamemaga.mooo.com:2096/subs/217f4ls7t6rrwoy0" {
		t.Fatalf("sub: %q", sub)
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
