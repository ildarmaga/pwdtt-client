//go:build !windows

package backend

func (a *App) DownloadAndApplyUpdate() UpdateApplyResult {
	return UpdateApplyResult{Message: "in-app update only on Windows"}
}

func (a *App) IsTunnelRunning() bool {
	return a.orch.IsRunning() || a.wb.IsRunning()
}

func (a *App) GetUpdateDownloadState() UpdateProgress { return UpdateProgress{} }

func (a *App) IsUpdateDownloading() bool { return false }
