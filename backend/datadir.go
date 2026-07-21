package backend

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wg-turn-client/core"
)

const (
	portableDataDirName = "data"
	migrateMarkerName   = ".migrated"
	migrateLogName      = "migrate.log"
)

var dataRoot string

// InitDataDir resolves <exe>/data (writable), migrates legacy AppData once,
// and points core + backend paths at the portable root.
// Call before VK-login worker / normal app start (not needed for --apply-update).
func InitDataDir() string {
	root := resolvePortableRoot()
	_ = os.MkdirAll(root, 0755)
	migrateLegacyInto(root)
	dataRoot = root
	core.SetConfigRoot(root)
	core.ReloadVKAuthSettings()
	return root
}

// DataDir returns the active portable data root (empty before InitDataDir).
func DataDir() string {
	if dataRoot != "" {
		return dataRoot
	}
	return core.ConfigRoot()
}

// GetDataDir is exposed to the UI.
func (a *App) GetDataDir() string {
	return DataDir()
}

func resolvePortableRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return core.ConfigRoot() // still legacy until SetConfigRoot
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	root := filepath.Join(filepath.Dir(exe), portableDataDirName)
	if err := os.MkdirAll(root, 0755); err != nil {
		return legacyAppDataPwdtt()
	}
	probe := filepath.Join(root, ".write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0600); err != nil {
		return legacyAppDataPwdtt()
	}
	_ = os.Remove(probe)
	return root
}

func legacyAppDataPwdtt() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = os.Getenv("HOME")
	}
	return filepath.Join(base, "pwdtt")
}

func legacyUpdateDir() string {
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		return filepath.Join(local, "WDTT", "update")
	}
	return ""
}

func migrateLegacyInto(root string) {
	migrateFromLegacy(root, legacyAppDataPwdtt(), legacyUpdateDir())
}

func migrateFromLegacy(root, legacy, oldUpdate string) {
	marker := filepath.Join(root, migrateMarkerName)
	if _, err := os.Stat(marker); err == nil {
		return
	}

	logPath := filepath.Join(root, migrateLogName)
	logf, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	logln := func(format string, args ...any) {
		if logf == nil {
			return
		}
		_, _ = fmt.Fprintf(logf, "[%s] "+format+"\n", append([]any{time.Now().UTC().Format(time.RFC3339)}, args...)...)
	}
	defer func() {
		if logf != nil {
			_ = logf.Close()
		}
	}()

	// Already has user data (fresh portable use) — just mark done.
	if dirHasEntries(filepath.Join(root, "profiles")) ||
		fileExists(filepath.Join(root, "secrets", "cookies-vk.json")) {
		logln("skip copy: portable data already present")
		_ = os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0600)
		return
	}

	if legacy != "" && samePath(legacy, root) {
		logln("skip: legacy root equals portable root")
		_ = os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0600)
		return
	}

	copied := 0
	if legacy != "" && dirExists(legacy) {
		logln("migrate from %s → %s", legacy, root)
		for _, sub := range []string{"profiles", "secrets", "settings", "logs", "webview-vk"} {
			n, err := copyDirContents(filepath.Join(legacy, sub), filepath.Join(root, sub))
			if err != nil {
				logln("WARN %s: %v", sub, err)
				continue
			}
			if n > 0 {
				logln("copied %s (%d files)", sub, n)
				copied += n
			}
		}
	} else {
		logln("no legacy AppData at %q", legacy)
	}

	if oldUpdate != "" && dirExists(oldUpdate) {
		n, err := copyDirContents(oldUpdate, filepath.Join(root, "update"))
		if err != nil {
			logln("WARN update: %v", err)
		} else if n > 0 {
			logln("copied update (%d files)", n)
			copied += n
		}
	}

	// Main Wails WebView2 profile (localStorage: servers/settings).
	exeName := ""
	if exe, err := os.Executable(); err == nil {
		exeName = filepath.Base(exe)
	}
	appData := os.Getenv("APPDATA")
	if appData == "" {
		appData = os.Getenv("HOME")
	}
	uiDst := filepath.Join(root, "webview-ui")
	if !dirHasEntries(uiDst) && appData != "" {
		candidates := []string{exeName, "wdtt-windows-amd64.exe", "wdtt.exe", "WDTT.exe", "pwdtt-desktop.exe"}
		for _, name := range candidates {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			src := filepath.Join(appData, name)
			if !dirExists(src) {
				continue
			}
			n, err := copyDirContents(src, uiDst)
			if err != nil {
				logln("WARN webview-ui from %s: %v", src, err)
				continue
			}
			if n > 0 {
				logln("copied webview-ui from %s (%d files)", src, n)
				copied += n
				break
			}
		}
	}

	logln("migration done, files=%d", copied)
	_ = os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0600)
}

func samePath(a, b string) bool {
	a, _ = filepath.Abs(a)
	b, _ = filepath.Abs(b)
	return filepath.Clean(a) == filepath.Clean(b)
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func dirHasEntries(path string) bool {
	ents, err := os.ReadDir(path)
	return err == nil && len(ents) > 0
}

func copyDirContents(src, dst string) (int, error) {
	if !dirExists(src) {
		return 0, nil
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		return 0, err
	}
	count := 0
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if fileExists(target) {
			return nil // keep existing portable file
		}
		if err := copyFile(path, target, info.Mode()); err != nil {
			return err
		}
		count++
		return nil
	})
	return count, err
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
