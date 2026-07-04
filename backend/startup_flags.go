package backend

// ShowWindowFlag is passed on relaunch after in-app update so the main window
// is visible instead of only the tray icon.
const ShowWindowFlag = "--show-window"

func WantsShowWindow(args []string) bool {
	for _, a := range args {
		if a == ShowWindowFlag {
			return true
		}
	}
	return false
}
