package vkcore

// VK is migrating vk.com → vk.ru (anton48 builds 169–171).
//
//   - vkCallJoinBase / vkWebHost: links and cookie-path endpoints we SEND to VK.
//     One-line revert: set host back to "vk.com" if a live test rejects vk.ru.
//   - User-facing parsers (normalizeVKJoinHash, UI) accept BOTH domains via
//     "/call/join/" path strip — do not hardcode a single host there.

const (
	vkWebHost      = "vk.ru"
	vkCallJoinBase = "https://vk.ru/call/join/"
)

func vkCallJoinURL(linkID string) string {
	return vkCallJoinBase + linkID
}

func vkWebOrigin() string  { return "https://" + vkWebHost }
func vkWebReferer() string { return "https://" + vkWebHost + "/" }
func vkLoginHost() string  { return "login." + vkWebHost }
func vkAPIHost() string    { return "api." + vkWebHost }
