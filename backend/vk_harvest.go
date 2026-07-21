package backend

import (
	"net/url"
	"strings"
)

// vkRemixsidIsNew reports whether found is a non-empty remixsid that appeared
// after the login window loaded (differs from the session baseline).
func vkRemixsidIsNew(found, baseline string) bool {
	return found != "" && found != baseline
}

// vkLoginCookiesReady — real VK login sets both remixsid and p; anonymous/guest
// remixsid during redirects must not close the window.
func vkLoginCookiesReady(remixsid, baseline, pCookie string) bool {
	return vkRemixsidIsNew(remixsid, baseline) && strings.TrimSpace(pCookie) != ""
}

// vkCookieDomainOK accepts session cookies on either .vk.com or .vk.ru
// (VK domain migration).
func vkCookieDomainOK(domain, suffixCOM, suffixRU string) bool {
	dom := strings.ToLower(domain)
	if !strings.HasPrefix(dom, ".") {
		dom = "." + dom
	}
	return strings.HasSuffix(dom, suffixCOM) || strings.HasSuffix(dom, suffixRU)
}

// vkLoginURLStillAuthFlow is true on explicit auth hosts / captcha / login paths.
func vkLoginURLStillAuthFlow(raw string) bool {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" || raw == "about:blank" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil {
		return true
	}
	host := u.Hostname()
	path := u.Path
	query := u.RawQuery

	switch {
	case strings.HasSuffix(host, "id.vk.ru"), strings.HasSuffix(host, "id.vk.com"):
		return true
	case strings.HasSuffix(host, "login.vk.ru"), strings.HasSuffix(host, "login.vk.com"):
		return true
	case strings.HasSuffix(host, "oauth.vk.ru"), strings.HasSuffix(host, "oauth.vk.com"):
		return true
	case strings.Contains(path, "not_robot"), strings.Contains(query, "not_robot"):
		return true
	case strings.Contains(query, "act=login"), strings.Contains(path, "/login"):
		return true
	}
	return false
}

// vkLoginURLLooksLoggedIn is a positive check: only harvest after leaving the
// login wall. VK shows QR on https://vk.ru/ (empty path) — that must NOT count
// as logged-in or the window closes before the user can scan.
func vkLoginURLLooksLoggedIn(raw string) bool {
	if vkLoginURLStillAuthFlow(raw) {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if !(strings.HasSuffix(host, "vk.ru") || strings.HasSuffix(host, "vk.com")) {
		return false
	}
	path := strings.Trim(u.Path, "/")
	if path == "" || path == "index.php" {
		return false
	}
	seg, _, _ := strings.Cut(path, "/")
	switch seg {
	case "feed", "im", "friends", "groups", "music", "video", "settings",
		"news", "clips", "notifications", "bookmarks", "apps", "market",
		"fave", "docs", "stories":
		return true
	}
	// Profile: id12345
	if strings.HasPrefix(seg, "id") && len(seg) > 2 {
		allDigit := true
		for _, r := range seg[2:] {
			if r < '0' || r > '9' {
				allDigit = false
				break
			}
		}
		if allDigit {
			return true
		}
	}
	return false
}
