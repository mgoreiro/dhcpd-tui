package app

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ServiceState es el estado de la unidad systemd.
type ServiceState struct {
	Available bool // systemctl existe
	Active    bool
	Enabled   bool
}

var (
	svcMu    sync.Mutex
	svcCache ServiceState
	svcAt    time.Time
)

func systemctl(args ...string) (string, error) {
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// ServiceState consulta (con caché de 3 s) el estado del servicio.
func (c *Core) ServiceState() ServiceState {
	svcMu.Lock()
	defer svcMu.Unlock()
	if time.Since(svcAt) < 3*time.Second {
		return svcCache
	}
	st := ServiceState{}
	if _, err := exec.LookPath("systemctl"); err == nil {
		st.Available = true
		a, _ := systemctl("is-active", c.ServiceName)
		e, _ := systemctl("is-enabled", c.ServiceName)
		st.Active, st.Enabled = a == "active", e == "enabled"
	}
	svcCache, svcAt = st, time.Now()
	return st
}

// ServiceCtl ejecuta start, stop, restart, enable o disable sobre la unidad.
func (c *Core) ServiceCtl(action string) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl no disponible en este sistema")
	}
	out, err := systemctl(action, c.ServiceName)
	svcMu.Lock()
	svcAt = time.Time{}
	svcMu.Unlock()
	if err != nil {
		if out == "" {
			out = err.Error()
		}
		return fmt.Errorf("systemctl %s %s: %s\n(¿ejecuta como root y está instalada la unidad?)", action, c.ServiceName, out)
	}
	c.Log.Logf("systemctl %s %s", action, c.ServiceName)
	return nil
}
