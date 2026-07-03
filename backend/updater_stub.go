//go:build !windows

package backend

func (a *App) DownloadAndApplyUpdate() UpdateApplyResult {
	return UpdateApplyResult{Message: "in-app update only on Windows"}
}
