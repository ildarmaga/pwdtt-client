//go:build windows

package wbjoiner

import _ "embed"

//go:embed bin/wbt-joiner-windows-amd64.exe
var binary []byte

// ExeName — имя файла при распаковке.
const ExeName = "wbt-joiner.exe"

// Binary возвращает встроенный бинарь WBT-joiner для текущей платформы.
func Binary() []byte { return binary }
