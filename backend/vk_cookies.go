package backend

import "wg-turn-client/core"

type VKCookiesStatus struct {
	OK      bool   `json:"ok"`
	Hint    string `json:"hint"`
	Path    string `json:"path"`
	Expired bool   `json:"expired"`
}

func (a *App) GetVKCookiesStatus() VKCookiesStatus {
	ok, hint := core.VKCookiesStatus()
	header, loadErr := core.LoadVKCookieHeader()
	expired := loadErr == nil && header != "" && !ok
	return VKCookiesStatus{
		OK:      ok,
		Hint:    hint,
		Path:    core.VKCookiesPathForUI(),
		Expired: expired,
	}
}

func (a *App) SaveVKCookies(payload string) error {
	return core.SaveVKCookiesJSON([]byte(payload))
}

func (a *App) ClearVKCookies() error {
	return core.ClearVKCookies()
}
