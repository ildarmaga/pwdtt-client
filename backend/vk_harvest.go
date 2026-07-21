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

// vkLoginURLStillAuthFlow is true while the user is still on VK ID / login / QR /
// captcha pages — harvesting here closes the window before they can finish.
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
