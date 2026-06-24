package backend

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"pwdtt-desktop/backend/wbjoiner"
)

// WBManager управляет WB Stream туннелем: запускает встроенный wbt-joiner
// подпроцессом, поднимает локальный SOCKS5 и транслирует его вывод в
// Wails-события (log / state_changed / tunnel_stats), как VK-оркестратор.
type WBManager struct {
	ctx   context.Context
	mu    sync.Mutex
	cmd   *exec.Cmd
	stdin io.WriteCloser
	done  chan struct{}
	stop  bool

	socksPort int
}

func NewWBManager(ctx context.Context) *WBManager {
	return &WBManager{ctx: ctx, socksPort: 1080}
}

// SocksAddr — адрес локального SOCKS5, который поднимает joiner.
func (m *WBManager) SocksAddr() string {
	return fmt.Sprintf("127.0.0.1:%d", m.socksPort)
}

// extractBinary распаковывает встроенный бинарь во временную папку и делает
// его исполняемым. Имя содержит хеш, чтобы переустанавливать при обновлении.
func extractWBJoiner() (string, error) {
	data := wbjoiner.Binary()
	if len(data) == 0 {
		return "", fmt.Errorf("WBT-joiner не встроен в сборку")
	}
	sum := sha256.Sum256(data)
	dir := filepath.Join(configDir(), "bin")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s", hex.EncodeToString(sum[:6]), wbjoiner.ExeName)
	path := filepath.Join(dir, name)
	if fi, err := os.Stat(path); err == nil && fi.Size() == int64(len(data)) {
		return path, nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

func (m *WBManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil
}

// Connect запускает WBT-joiner для указанной комнаты (wbstream://<id>).
func (m *WBManager) Connect(room string) error {
	room = strings.TrimSpace(room)
	if room == "" {
		return fmt.Errorf("не задана WB-комната (wb_room) — обновите подписку")
	}
	m.mu.Lock()
	if m.cmd != nil {
		m.mu.Unlock()
		return fmt.Errorf("WB туннель уже запущен")
	}
	m.stop = false
	m.mu.Unlock()

	bin, err := extractWBJoiner()
	if err != nil {
		return err
	}
	// Кладём wintun.dll рядом с joiner-ом, иначе tun2socks не поднимет TUN
	// (на Windows). На других ОС — no-op.
	if err := placeWintunNextTo(filepath.Dir(bin)); err != nil {
		m.emitLog("WARN", fmt.Sprintf("wintun.dll не распакован (TUN может не подняться): %v", err))
	}

	m.emitLog("INFO", "Подключение WB Stream…")
	m.emitLog("GO", fmt.Sprintf("wb: room %s · socks 127.0.0.1:%d", maskRoom(room), m.socksPort))
	runtime.EventsEmit(m.ctx, "state_changed", "connecting")

	cmd := exec.Command(bin,
		"--room", room,
		"--socks-host", "127.0.0.1",
		"--socks-port", strconv.Itoa(m.socksPort),
		"--name", "WDTT",
		"--tun",
	)
	hideConsoleWindow(cmd) // прячем окно консоли подпроцесса (Windows)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// stdin используется для graceful-shutdown: закрытие пайпа просит joiner
	// корректно снять TUN/маршруты (на Windows SIGTERM недоступен).
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		runtime.EventsEmit(m.ctx, "state_changed", "error")
		return fmt.Errorf("не удалось запустить WB-joiner: %w", err)
	}

	done := make(chan struct{})
	m.mu.Lock()
	m.cmd = cmd
	m.stdin = stdin
	m.done = done
	m.mu.Unlock()

	go m.readOutput(stdout)
	go func() {
		_ = cmd.Wait()
		close(done)
		m.mu.Lock()
		stopped := m.stop
		m.cmd = nil
		m.stdin = nil
		m.mu.Unlock()
		if !stopped {
			m.emitLog("WARN", "WB туннель завершился")
		}
		m.emitLog("INFO", "— Отключено")
		runtime.EventsEmit(m.ctx, "state_changed", "stopped")
	}()
	return nil
}

// Disconnect останавливает WBT-joiner. Сначала просит его корректно
// завершиться (закрываем stdin → joiner снимает TUN/маршруты), и только
// если он не успел — убиваем принудительно. Это критично на Windows:
// жёсткий Kill оставил бы wintun-адаптер и default-маршруты, и интернет
// остался бы сломанным после отключения.
func (m *WBManager) Disconnect() {
	m.mu.Lock()
	m.stop = true
	cmd := m.cmd
	stdin := m.stdin
	done := m.done
	m.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	if stdin != nil {
		_, _ = io.WriteString(stdin, "stop\n")
		_ = stdin.Close()
	}
	if done == nil {
		_ = cmd.Process.Kill()
		return
	}
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		_ = cmd.Process.Kill()
	}
}

func (m *WBManager) readOutput(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r\n")
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "STATS "):
			m.handleStats(line)
		case strings.Contains(line, "STATUS:TUNNEL_CONNECTED"):
			runtime.EventsEmit(m.ctx, "state_changed", "running")
			m.emitLog("STATUS", fmt.Sprintf("WB туннель активен · SOCKS5 %s", m.SocksAddr()))
		case strings.Contains(line, "TUN ACTIVE"),
			strings.Contains(line, "STATUS:TUN_ACTIVE"):
			m.emitLog("STATUS", "Полный VPN активен — весь трафик через WB Stream")
		case strings.Contains(line, "STATUS:TUN_UNAVAILABLE"):
			m.emitLog("WARN", "TUN недоступен — работает только SOCKS5 (проверьте права администратора)")
		case strings.Contains(line, "STATUS:SOCKS_PORT:"):
			m.handleSocksPort(line)
		case strings.Contains(line, "STATUS:SOCKS_BIND_FAILED"):
			m.emitLog("ERROR", fmt.Sprintf("Порт SOCKS5 %s занят — отключитесь и подключитесь заново", m.SocksAddr()))
			runtime.EventsEmit(m.ctx, "state_changed", "error")
		default:
			m.emitLog("GO", line)
		}
	}
}

// handleSocksPort обновляет фактический порт SOCKS5, который joiner выбрал
// (при занятом порте он берёт свободный сам). Порт внутренний — его использует
// tun2socks внутри joiner-а, — поэтому достаточно отразить его в UI/логах.
func (m *WBManager) handleSocksPort(line string) {
	idx := strings.LastIndex(line, "STATUS:SOCKS_PORT:")
	if idx < 0 {
		return
	}
	portStr := strings.TrimSpace(line[idx+len("STATUS:SOCKS_PORT:"):])
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return
	}
	m.mu.Lock()
	changed := port != m.socksPort
	m.socksPort = port
	m.mu.Unlock()
	if changed {
		m.emitLog("INFO", fmt.Sprintf("SOCKS5 порт занят — joiner использует свободный %d", port))
	}
}

func (m *WBManager) handleStats(line string) {
	var rx, tx, rtt, fps int64
	for _, kv := range strings.Fields(strings.TrimPrefix(line, "STATS ")) {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		v, _ := strconv.ParseInt(parts[1], 10, 64)
		switch parts[0] {
		case "rx":
			rx = v
		case "tx":
			tx = v
		case "rtt":
			rtt = v
		case "fps":
			fps = v
		}
	}
	// Совпадает с обработчиком VK tunnel_stats: rx, tx, workers, turnRtt, dtlsHs, internetRtt.
	// Для WB: WBT = rtt, VP8 = fps (фронт рендерит как «N fps»), RTT = rtt.
	runtime.EventsEmit(m.ctx, "tunnel_stats", rx, tx, 1, rtt, fps, rtt)
}

func (m *WBManager) emitLog(level, msg string) {
	runtime.EventsEmit(m.ctx, "log", level, msg)
}

func maskRoom(room string) string {
	id := room
	if i := strings.LastIndex(room, "/"); i >= 0 {
		id = room[i+1:]
	}
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "…"
}
