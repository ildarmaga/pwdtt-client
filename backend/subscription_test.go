package backend

import (
	"encoding/base64"
	"net/http"
	"testing"
)

func TestParseJSONWdttLink(t *testing.T) {
	link := "wdtt://eyJwcyI6ImlsZGFyIiwiaXAiOiJkZXZnYW1lbWFnYS5tb29vLmNvbSIsImR0bHMiOjU2MDAwLCJwYXNzIjoiZjFrLTIzXHUwMDI2LTg5QCJ9"
	p, err := parseWdttLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if p.IP != "devgamemaga.mooo.com" || p.DtlsPort != "56000" || p.Password != "f1k-23&-89@" || p.Name != "ildar" {
		t.Fatalf("unexpected parse: %+v", p)
	}
}

func TestParseColonWdttLink(t *testing.T) {
	p, err := parseWdttLink("wdtt://1.2.3.4:56000:56001:0:secret#Test")
	if err != nil {
		t.Fatal(err)
	}
	if p.IP != "1.2.3.4" || p.Password != "secret" || p.Name != "Test" {
		t.Fatalf("unexpected parse: %+v", p)
	}
}

func TestParseJSONWdttLinkWithSub(t *testing.T) {
	payload := `{"ps":"PC-ildar","ip":"devgamemaga.mooo.com","dtls":56000,"pass":"ildar123I","sub":"https://devgamemaga.mooo.com:2096/sub/abc123"}`
	link := "wdtt://" + base64.StdEncoding.EncodeToString([]byte(payload))
	p, err := parseWdttLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if p.SubURL != "https://devgamemaga.mooo.com:2096/sub/abc123" {
		t.Fatalf("unexpected sub url: %+v", p)
	}
}

func TestParseSubUserInfo(t *testing.T) {
	s := parseSubUserInfo("upload=100; download=200; total=1000; expire=0")
	if s == nil || s.Upload != 100 || s.Download != 200 || s.Total != 1000 {
		t.Fatalf("unexpected stats: %+v", s)
	}
}

func TestParseSubResponseStats(t *testing.T) {
	h := http.Header{}
	h.Set("Subscription-Userinfo", "upload=100; download=200; total=0; expire=1717459200")
	h.Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte("MAGIC VPN")))
	h.Set("Announce", "base64:"+base64.StdEncoding.EncodeToString([]byte("Самый лучший VPN")))
	h.Set("Profile-Web-Page-Url", "https://devgamemaga.mooo.com/")
	h.Set("Profile-Update-Interval", "12")
	s := parseSubResponseStats(h)
	if s == nil || s.Title != "MAGIC VPN" || s.Announce != "Самый лучший VPN" {
		t.Fatalf("unexpected meta: %+v", s)
	}
	if s.SupportURL != "https://devgamemaga.mooo.com/" || s.UpdateInterval != 12 {
		t.Fatalf("unexpected urls/interval: %+v", s)
	}
}
