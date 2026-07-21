package backend

import "wg-turn-client/core"

type VKCookiesStatus struct {
	OK         bool   `json:"ok"`
	Hint       string `json:"hint"`
	Path       string `json:"path"`
	Expired    bool   `json:"expired"`
	UseCookies bool   `json:"useCookies"`
}

func (a *App) GetVKUseCookies() bool {
	return core.VKUseCookies()
}

func (a *App) SetVKUseCookies(v bool) error {
	return core.SetVKUseCookies(v)
}

func (a *App) GetVKCookiesStatus() VKCookiesStatus {
	useCookies := core.VKUseCookies()
	ok, hint := core.VKCookiesStatus()
	header, loadErr := core.LoadVKCookieHeader()
	hasFile := loadErr == nil && header != ""
	expired := hasFile && !ok
	return VKCookiesStatus{
		OK:         ok,
		Hint:       hint,
		Path:       core.VKCookiesPathForUI(),
		Expired:    expired,
		UseCookies: useCookies,
	}
}

// GetVKCookiesRaw returns cookies-vk.json contents for the Settings textarea.
func (a *App) GetVKCookiesRaw() (string, error) {
	return core.ReadVKCookiesRaw()
}

func (a *App) SaveVKCookies(payload string) error {
	return core.SaveVKCookiesJSON([]byte(payload))
}

func (a *App) ClearVKCookies() error {
	return core.ClearVKCookies()
}
