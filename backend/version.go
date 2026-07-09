package backend

// AppVersion is injected at build time via -ldflags "-X pwdtt-desktop/backend.AppVersion=...".
var AppVersion = "0.3.204"

func (a *App) GetAppVersion() string {
	if AppVersion == "" {
		return "dev"
	}
	return AppVersion
}
