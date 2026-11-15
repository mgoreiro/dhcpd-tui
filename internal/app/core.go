// Package app une configuración, base de leases y servidor DHCP.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dhcpd-tui/internal/config"
	"dhcpd-tui/internal/dhcp"
)

// LogBuf es un registro circular en memoria.
type LogBuf struct {
	mu    sync.Mutex
	lines []string
	ver   int
	Echo  *os.File // si no es nil, también se escribe aquí (modo headless)
}

func (l *LogBuf) Logf(format string, a ...any) {
	line := time.Now().Format("15:04:05 ") + fmt.Sprintf(format, a...)
	l.mu.Lock()
	l.lines = append(l.lines, line)
	if len(l.lines) > 500 {
		l.lines = l.lines[len(l.lines)-500:]
	}
	l.ver++
	echo := l.Echo
	l.mu.Unlock()
	if echo != nil {
		fmt.Fprintln(echo, line)
	}
}

// Tail devuelve las últimas n líneas y un número de versión.
func (l *LogBuf) Tail(n int) ([]string, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.lines
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return append([]string(nil), s...), l.ver
}

// Core es el estado compartido de la aplicación.
type Core struct {
	Dir       string
	CfgPath   string
	LeasePath string
	// ServerMode=false: solo se edita configuración/consulta leases; otro
	// proceso (-headless) sirve DHCP y recoge los cambios del fichero.
	ServerMode bool
	Srv        *dhcp.Server
	DB         *dhcp.DB
	Log        *LogBuf
	LastError  string
	// ServiceName es la unidad systemd que se controla en modo -no-server.
	ServiceName string

	mu      sync.Mutex
	cfg     *config.Config
	lastMod time.Time
}

func NewCore(dir string, serverMode bool, serverPort, clientPort int) (*Core, error) {
	c := &Core{
		Dir: dir, CfgPath: filepath.Join(dir, "config.json"), LeasePath: filepath.Join(dir, "leases.json"),
		ServerMode: serverMode, Log: &LogBuf{}, ServiceName: "dhcpd-tui",
	}
	cfg, created, err := config.Load(c.CfgPath)
	if err != nil {
		return nil, err
	}
	c.DB = dhcp.NewDB(c.LeasePath)
	if err := c.DB.Load(); err != nil {
		return nil, fmt.Errorf("leases: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		if created {
			// La configuración por defecto puede no cuadrar con la red local; se
			// deja editable en la TUI.
			cfg = config.Default()
			cfg.Subnet, cfg.RangeStart, cfg.RangeEnd, cfg.Options.Routers = "192.168.1.0/24", "192.168.1.100", "192.168.1.199", []string{"192.168.1.1"}
		} else {
			return nil, fmt.Errorf("%s: %w", c.CfgPath, err)
		}
	}
	c.Srv, err = dhcp.New(cfg, c.DB, c.Log.Logf)
	if err != nil {
		return nil, err
	}
	c.Srv.ServerPort, c.Srv.ClientPort = serverPort, clientPort
	c.cfg = cfg
	if created {
		if err := config.Save(c.CfgPath, cfg); err == nil {
			c.Log.Logf("Creada configuración inicial en %s", c.CfgPath)
		}
	}
	c.lastMod = modTime(c.CfgPath)
	return c, nil
}

func modTime(p string) time.Time {
	if st, err := os.Stat(p); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}

// Config devuelve una copia editable de la configuración vigente.
func (c *Core) Config() *config.Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.Clone()
}

// Apply valida, aplica en caliente y guarda en disco.
func (c *Core) Apply(cfg *config.Config) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := c.Srv.SetConfig(cfg); err != nil {
		return err
	}
	if err := config.Save(c.CfgPath, cfg); err != nil {
		return fmt.Errorf("no se pudo guardar: %w", err)
	}
	c.cfg = cfg.Clone()
	c.lastMod = modTime(c.CfgPath)
	return nil
}

// Start inicia el servidor DHCP.
func (c *Core) Start() error {
	if !c.ServerMode {
		return fmt.Errorf("modo solo-configuración: el servidor se ejecuta en otro proceso (-headless)")
	}
	err := c.Srv.Start()
	if err != nil {
		c.LastError = err.Error()
		c.Log.Logf("ERROR: %v", err)
	} else {
		c.LastError = ""
	}
	return err
}

func (c *Core) Stop() { c.Srv.Stop() }

// ForgetLease elimina una concesión.
func (c *Core) ForgetLease(mac string) { c.DB.Delete(mac) }

// Watch recarga la configuración si cambia en disco y, en modo solo-TUI,
// los leases escritos por el proceso servidor.
func (c *Core) Watch(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !c.ServerMode {
				c.DB.Reload()
			}
			if mt := modTime(c.CfgPath); !mt.IsZero() {
				c.mu.Lock()
				changed := !mt.Equal(c.lastMod)
				c.mu.Unlock()
				if changed {
					c.Reload()
				}
			}
		}
	}
}

// Reload relee config.json y lo aplica.
func (c *Core) Reload() error {
	cfg, _, err := config.Load(c.CfgPath)
	if err == nil {
		err = cfg.Validate()
	}
	c.mu.Lock()
	c.lastMod = modTime(c.CfgPath)
	c.mu.Unlock()
	if err != nil {
		c.Log.Logf("Configuración en disco no válida, se mantiene la actual: %v", err)
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.Srv.SetConfig(cfg); err != nil {
		c.Log.Logf("No se pudo aplicar la configuración: %v", err)
		return err
	}
	c.cfg = cfg.Clone()
	c.Log.Logf("Configuración recargada desde disco")
	return nil
}

// DefaultDir: /etc/dhcpd-tui como root, ./dhcpd-data en otro caso.
func DefaultDir() string {
	if os.Geteuid() == 0 {
		return "/etc/dhcpd-tui"
	}
	return "dhcpd-data"
}

func Describe(cfg *config.Config) string {
	return strings.TrimSpace(fmt.Sprintf("%s %s", cfg.Interface, cfg.Subnet))
}
