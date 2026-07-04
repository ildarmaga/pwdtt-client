package backend

// vkRemixsidIsNew reports whether found is a non-empty remixsid that appeared
// after the login window loaded vk.com (differs from the session baseline).
func vkRemixsidIsNew(found, baseline string) bool {
	return found != "" && found != baseline
}
