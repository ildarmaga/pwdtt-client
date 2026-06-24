//go:build linux

package wbjoiner

import _ "embed"

//go:embed bin/wbt-joiner-linux-amd64
var binary []byte

// ExeName — имя файла при распаковке.
const ExeName = "wbt-joiner"

// Binary возвращает встроенный бинарь WBT-joiner для текущей платформы.
func Binary() []byte { return binary }
