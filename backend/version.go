package backend

// AppVersion is injected at build time via -ldflags "-X pwdtt-desktop/backend.AppVersion=...".
var AppVersion = "0.3.235"

func (a *App) GetAppVersion() string {
	if AppVersion == "" {
		return "dev"
	}
	return AppVersion
}
