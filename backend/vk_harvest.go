package backend

import "strings"

// vkRemixsidIsNew reports whether found is a non-empty remixsid that appeared
// after the login window loaded vk.com (differs from the session baseline).
func vkRemixsidIsNew(found, baseline string) bool {
	return found != "" && found != baseline
}

// vkLoginCookiesReady — real VK login sets both remixsid and p; anonymous/guest
// remixsid during redirects must not close the window.
func vkLoginCookiesReady(remixsid, baseline, pCookie string) bool {
	return vkRemixsidIsNew(remixsid, baseline) && strings.TrimSpace(pCookie) != ""
}
