package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"dhcpd-tui/internal/app"
	"dhcpd-tui/internal/config"
	"dhcpd-tui/internal/dhcp"
)

func dump(sim tcell.SimulationScreen) string {
	cells, w, h := sim.GetContents()
	var sb strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) > 0 {
				sb.WriteRune(c.Runes[0])
			} else {
				sb.WriteRune(' ')
			}
		}
		sb.WriteRune('\n')
	}
	return sb.String()
}

func newTestApp(t *testing.T) (*App, tcell.SimulationScreen, *app.Core) {
	t.Helper()
	core, err := app.NewCore(t.TempDir(), false, 6767, 6868)
	if err != nil {
		t.Fatal(err)
	}
	cfg := core.Config()
	cfg.Interface, cfg.ServerIP, cfg.Subnet = "eth0", "192.168.1.1", "192.168.1.0/24"
	cfg.RangeStart, cfg.RangeEnd = "192.168.1.100", "192.168.1.199"
	cfg.Options.Routers = []string{"192.168.1.1"}
	cfg.Clients = []config.Client{{MAC: "aa:bb:cc:00:00:01", Name: "nas", IP: "192.168.1.50", Options: config.Options{DNS: []string{"9.9.9.9"}}}}
	if err := core.Apply(cfg); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	core.DB.Commit(dhcp.Lease{MAC: "de:ad:be:ef:00:01", IP: "192.168.1.100", Hostname: "portatil", Start: now, Expiry: now.Add(5*time.Hour + 20*time.Minute)})
	core.DB.Commit(dhcp.Lease{MAC: "de:ad:be:ef:00:02", IP: "192.168.1.101", Hostname: "movil", Start: now.Add(-9 * time.Hour), Expiry: now.Add(-3 * time.Hour)})
	core.Log.Logf("DISCOVER de:ad:be:ef:00:01 -> OFFER 192.168.1.100")
	core.Log.Logf("REQUEST de:ad:be:ef:00:01 192.168.1.100 -> ACK (6h)")

	a := newApp(core)
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(100, 30)
	a.tv.SetScreen(sim)
	a.refresh()
	go a.tv.Run()
	time.Sleep(300 * time.Millisecond)
	t.Cleanup(func() { a.tv.Stop() })
	return a, sim, core
}

func key(a *App, sim tcell.SimulationScreen, k tcell.Key, r rune, mod tcell.ModMask) {
	sim.InjectKey(k, r, mod)
	time.Sleep(250 * time.Millisecond)
}

func TestScreens(t *testing.T) {
	a, sim, core := newTestApp(t)
	t.Log("\n" + dump(sim))
	main := dump(sim)
	for _, want := range []string{"Clientes", "portatil", "192.168.1.100", "Activo", "Expirado", "IP fija", "6h", "SOLO CONFIG"} {
		if !strings.Contains(main, want) {
			t.Errorf("pantalla principal sin %q", want)
		}
	}

	key(a, sim, tcell.KeyF2, 0, 0)
	t.Log("\n" + dump(sim))
	if !strings.Contains(dump(sim), "Configuración del servidor") {
		t.Error("F2 no abre el diálogo")
	}
	key(a, sim, tcell.KeyEscape, 0, 0)

	key(a, sim, tcell.KeyF3, 0, 0)
	t.Log("\n" + dump(sim))
	key(a, sim, tcell.KeyEscape, 0, 0)

	key(a, sim, tcell.KeyF10, 0, 0)
	t.Log("\n" + dump(sim))
	key(a, sim, tcell.KeyEscape, 0, 0)

	// Editar cliente con Intro (primera fila seleccionable = .50 nas)
	key(a, sim, tcell.KeyEnter, 0, 0)
	t.Log("\n" + dump(sim))
	if !strings.Contains(dump(sim), "Cliente aa:bb:cc:00:00:01") {
		t.Error("Intro no abre el diálogo del cliente")
	}
	key(a, sim, tcell.KeyEscape, 0, 0)

	// Reservar la IP actual del portátil (F6): bajar a su fila.
	key(a, sim, tcell.KeyDown, 0, 0)
	key(a, sim, tcell.KeyF6, 0, 0)
	if cl := core.Config().FindClient("de:ad:be:ef:00:01"); cl == nil || cl.IP != "192.168.1.100" || cl.Name != "" && cl.Name != "portatil" {
		t.Errorf("reserva no creada: %+v", cl)
	}
	t.Log("\n" + dump(sim))
	key(a, sim, tcell.KeyF7, 0, 0) // bloquear
	if cl := core.Config().FindClient("de:ad:be:ef:00:01"); cl == nil || !cl.Deny {
		t.Error("bloqueo no aplicado")
	}
}
