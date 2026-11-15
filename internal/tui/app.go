package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"dhcpd-tui/internal/app"
	"dhcpd-tui/internal/config"
	"dhcpd-tui/internal/dhcp"
)

type row struct {
	mac, ip, name, vendor string
	lease                 *dhcp.Lease
	cl                    *config.Client
}

// App es la interfaz Turbo Vision.
type App struct {
	core    *app.Core
	tv      *tview.Application
	pages   *tview.Pages
	menubar *MenuBar
	bar     *StatusBar
	status  *tview.TextView
	table   *tview.Table
	logv    *tview.TextView
	rows    []row
	modals  []string
	logVer  int
	menuIdx int
	screen  tcell.Screen
}

// Run arranca la TUI y bloquea hasta salir.
func Run(core *app.Core) error {
	return newApp(core).run()
}

func newApp(core *app.Core) *App {
	applyTheme()
	a := &App{core: core, tv: tview.NewApplication(), pages: tview.NewPages(), menuIdx: -1}
	a.buildWidgets()
	return a
}

func (a *App) buildWidgets() {
	// Ventana de estado
	a.status = tview.NewTextView().SetDynamicColors(true)
	a.status.SetBorder(true).SetTitle(" Servidor DHCP ").SetBorderPadding(0, 0, 1, 1)

	// Ventana de clientes
	a.table = tview.NewTable().SetSelectable(true, false).SetFixed(1, 0).SetSeparator(' ')
	a.table.SetSelectedStyle(tcell.StyleDefault.Foreground(tvBlack).Background(tvGreen))
	a.table.SetBorder(true).SetTitle(" Clientes ")
	a.table.SetSelectedFunc(func(r, c int) { a.editSelected() })

	// Ventana de registro
	a.logv = tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	a.logv.SetBorder(true).SetTitle(" Registro ").SetBorderPadding(0, 0, 1, 1)

	desk := newDesktop(a.status, a.table, a.logv, a.table)

	a.menubar = newMenuBar(a.menus())
	a.menubar.onClick = a.openMenu
	a.menubar.right = func() string {
		if a.core.Srv.Running() {
			return "● ACTIVO "
		}
		return "○ DETENIDO "
	}
	a.menubar.right = func() string {
		if !a.core.ServerMode {
			if a.core.ServiceState().Active {
				return "● SERVICIO ACTIVO "
			}
			return "○ SERVICIO PARADO "
		}
		if a.core.Srv.Running() {
			return "● ACTIVO "
		}
		return "○ DETENIDO "
	}
	a.bar = &StatusBar{Box: tview.NewBox(), keys: [][2]string{
		{"F1", " Ayuda"}, {"F2", " Servidor"}, {"F3", " Opciones"}, {"F4", " Editar"}, {"F5", " Inic/Parar"},
		{"F6", " Reservar"}, {"F7", " Bloq"}, {"F8", " Olvidar"}, {"F9", " Arranque"}, {"Ins", " Nuevo"}, {"Alt-X", " Salir"},
	}}

	a.pages.AddPage("main", desk, true, true)
	root := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.menubar, 1, 0, false).
		AddItem(a.pages, 0, 1, true).
		AddItem(a.bar, 1, 0, false)
	a.tv.SetRoot(root, true).EnableMouse(true).SetFocus(a.pages)
	a.tv.SetInputCapture(a.globalKeys)
}

func (a *App) run() error {
	if a.core.ServerMode && a.core.Config().Autostart {
		if err := a.core.Start(); err != nil {
			a.message("Error al iniciar el servidor", err.Error()+"\n\nPuede corregir la configuración con F2.")
		}
	}
	a.refresh()
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				a.tv.QueueUpdateDraw(a.refresh)
			}
		}
	}()
	return a.tv.Run()
}

// ------------------------------------------------------------------ teclas

func (a *App) globalKeys(ev *tcell.EventKey) *tcell.EventKey {
	if len(a.modals) > 0 {
		return ev // los diálogos gestionan sus propias teclas
	}
	if ev.Modifiers()&tcell.ModAlt != 0 && ev.Key() == tcell.KeyRune {
		r := strings.ToLower(string(ev.Rune()))
		if r == "x" {
			a.quit()
			return nil
		}
		for i, m := range a.menubar.menus {
			if _, h, _ := splitHot(m.title); strings.ToLower(string(h)) == r {
				a.openMenu(i)
				return nil
			}
		}
	}
	switch ev.Key() {
	case tcell.KeyF1:
		a.help()
	case tcell.KeyF2:
		a.serverDialog()
	case tcell.KeyF3:
		a.optionsDialog()
	case tcell.KeyF4:
		a.editSelected()
	case tcell.KeyF5:
		a.toggleServer()
	case tcell.KeyF6:
		a.reserveCurrent()
	case tcell.KeyF7:
		a.toggleBlock()
	case tcell.KeyF8:
		a.forgetLease()
	case tcell.KeyF9:
		a.toggleBoot()
	case tcell.KeyF10:
		a.openMenu(0)
	case tcell.KeyInsert:
		a.clientDialog("", true)
	case tcell.KeyDelete:
		a.deleteProfile()
	case tcell.KeyCtrlQ:
		a.quit()
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'e':
			a.editSelected()
		case 'r':
			a.reserveCurrent()
		case 'b':
			a.toggleBlock()
		case 'n':
			a.clientDialog("", true)
		case 'f':
			a.forgetLease()
		case 's':
			a.toggleServer()
		case 'c':
			a.serverDialog()
		case 'o':
			a.optionsDialog()
		case 'q':
			a.quit()
		case '?':
			a.help()
		default:
			return ev
		}
	default:
		return ev
	}
	return nil
}

// ------------------------------------------------------------------ menús

func (a *App) menus() []menu {
	return []menu{
		{"&Archivo", []menuItem{
			{label: "&Recargar configuración", action: func() { a.reload() }},
			{label: "&Salir", key: "Alt-X", action: a.quit},
		}},
		{"&Servidor", []menuItem{
			{label: "&Iniciar / Parar", key: "F5", action: a.toggleServer},
			{label: "&Arrancar con el sistema", key: "F9", action: a.toggleBoot},
			{label: "&Configuración general...", key: "F2", action: a.serverDialog},
			{label: "&Opciones DHCP...", key: "F3", action: a.optionsDialog},
		}},
		{"&Clientes", []menuItem{
			{label: "&Editar / configurar...", key: "F4", action: a.editSelected},
			{label: "&Reservar IP actual", key: "F6", action: a.reserveCurrent},
			{label: "&Bloquear / permitir", key: "F7", action: a.toggleBlock},
			{label: "&Nuevo cliente...", key: "Ins", action: func() { a.clientDialog("", true) }},
			{label: "&Olvidar lease", key: "F8", action: a.forgetLease},
			{label: "&Eliminar perfil", key: "Del", action: a.deleteProfile},
		}},
		{"A&yuda", []menuItem{
			{label: "&Teclas y ayuda", key: "F1", action: a.help},
			{label: "&Acerca de...", action: a.about},
		}},
	}
}

func (a *App) closeMenu() {
	if a.menuIdx >= 0 {
		a.pages.RemovePage("menu")
		a.modals = a.modals[:len(a.modals)-1]
		a.menuIdx = -1
		a.menubar.active = -1
		a.tv.SetFocus(a.pages)
	}
}

func (a *App) openMenu(i int) {
	if a.menuIdx >= 0 {
		a.closeMenu()
	} else if len(a.modals) > 0 {
		return
	}
	mn := a.menus()[i]
	a.menuIdx, a.menubar.active = i, i
	width := 0
	for _, it := range mn.items {
		b, h, af := splitHot(it.label)
		width = max(width, len([]rune(b+string(h)+af))+len(it.key)+3)
	}
	list := tview.NewList().ShowSecondaryText(false)
	list.SetBackgroundColor(tvGrey).SetBorder(true).SetBorderColor(tvBlack)
	list.SetMainTextColor(tvBlack).SetSelectedTextColor(tvWhite).SetSelectedBackgroundColor(tvGreen)
	list.SetShortcutColor(tvRed)
	for _, it := range mn.items {
		b, h, af := splitHot(it.label)
		name := b + string(h) + af
		pad := width - len([]rune(name)) - len(it.key) - 2
		text := " " + name + strings.Repeat(" ", max(pad, 1)) + it.key
		act := it.action
		list.AddItem(text, "", 0, func() {
			a.closeMenu()
			act()
		})
	}
	list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyEscape, tcell.KeyF10:
			a.closeMenu()
			return nil
		case tcell.KeyLeft:
			a.openMenu((i + len(a.menubar.menus) - 1) % len(a.menubar.menus))
			return nil
		case tcell.KeyRight:
			a.openMenu((i + 1) % len(a.menubar.menus))
			return nil
		case tcell.KeyRune:
			for idx, it := range mn.items {
				if _, h, _ := splitHot(it.label); h != 0 && strings.EqualFold(string(h), string(ev.Rune())) {
					list.SetCurrentItem(idx)
					a.closeMenu()
					it.action()
					return nil
				}
			}
		}
		return ev
	})
	list.SetMouseCapture(func(act tview.MouseAction, ev *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		return act, ev
	})
	ov := newOverlay(list, width+2, len(mn.items)+2)
	ov.anchored, ov.ax, ov.ay = true, a.menubar.pos[i], 1
	ov.onOutsideClk = a.closeMenu
	a.pages.AddPage("menu", ov, true, true)
	a.modals = append(a.modals, "menu")
	a.tv.SetFocus(ov)
}

// ------------------------------------------------------------------ modales

func (a *App) pushModal(name string, p tview.Primitive, w, h int) *Overlay {
	_, _, sw, sh := a.pages.GetRect()
	if sw == 0 {
		sw, sh = 80, 24
	}
	w, h = min(w, sw-2), min(h, sh-1)
	ov := newOverlay(p, w, h)
	a.pages.AddPage(name, ov, true, true)
	a.modals = append(a.modals, name)
	a.tv.SetFocus(ov)
	return ov
}

func (a *App) popModal() {
	if len(a.modals) == 0 {
		return
	}
	name := a.modals[len(a.modals)-1]
	a.modals = a.modals[:len(a.modals)-1]
	a.pages.RemovePage(name)
	a.tv.SetFocus(a.pages)
}

var modalSeq int

func (a *App) nextName(p string) string {
	modalSeq++
	return fmt.Sprintf("%s-%d", p, modalSeq)
}

// dialogBox crea un marco gris con título.
func dialogBox(title string) *tview.Box {
	b := tview.NewBox().SetBackgroundColor(tvGrey)
	b.SetBorder(true).SetBorderColor(tvWhite).SetTitle(" " + title + " ").SetTitleColor(tvBlack)
	return b
}

func styleForm(f *tview.Form, title string) {
	f.SetBackgroundColor(tvGrey).SetBorder(true).SetBorderColor(tvWhite).SetTitle(" " + title + " ").SetTitleColor(tvBlack)
	f.SetItemPadding(0).SetLabelColor(tvBlack)
	f.SetFieldBackgroundColor(tvBlue).SetFieldTextColor(tvWhite)
	f.SetButtonBackgroundColor(tvGreen).SetButtonTextColor(tvBlack)
	f.SetButtonActivatedStyle(tcell.StyleDefault.Foreground(tvWhite).Background(tvBlack))
	f.SetButtonsAlign(tview.AlignCenter)
}

// message muestra un aviso con botón Aceptar.
func (a *App) message(title, text string) {
	a.ask(title, text, []string{"Aceptar"}, nil)
}

// confirm pregunta Sí/No.
func (a *App) confirm(title, text string, yes func()) {
	a.ask(title, text, []string{"Sí", "No"}, func(i int) {
		if i == 0 {
			yes()
		}
	})
}

func (a *App) ask(title, text string, buttons []string, done func(i int)) {
	lines := strings.Split(text, "\n")
	w := 36
	for _, l := range lines {
		w = max(w, min(len([]rune(l))+6, 70))
	}
	h := 5
	for _, l := range lines {
		h += max(1, (len([]rune(l))+w-7)/(w-6))
	}
	tv := tview.NewTextView().SetText(text).SetTextColor(tvBlack).SetDynamicColors(false)
	tv.SetBackgroundColor(tvGrey).SetBorderPadding(0, 0, 1, 1)
	f := tview.NewForm()
	f.SetBackgroundColor(tvGrey)
	f.SetButtonBackgroundColor(tvGreen).SetButtonTextColor(tvBlack)
	f.SetButtonActivatedStyle(tcell.StyleDefault.Foreground(tvWhite).Background(tvBlack)).SetButtonsAlign(tview.AlignCenter)
	for i, b := range buttons {
		i := i
		f.AddButton(b, func() {
			a.popModal()
			if done != nil {
				done(i)
			}
		})
	}
	f.SetCancelFunc(a.popModal)
	box := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(tv, 0, 1, false).AddItem(f, 3, 0, true)
	box.SetBackgroundColor(tvGrey).SetBorder(true).SetBorderColor(tvWhite).SetTitle(" " + title + " ").SetTitleColor(tvBlack)
	a.pushModal(a.nextName("msg"), box, w, h)
}

func (a *App) quit() {
	a.confirm("Salir", "¿Salir de la aplicación?\nSe detendrá el servidor DHCP.", func() { a.tv.Stop() })
}

func (a *App) about() {
	a.message("Acerca de", "DHCPD-TUI\nServidor DHCP autónomo con interfaz Turbo Vision\nBinario único para Linux ARM/ARM64/x86\n\nDatos en: "+a.core.Dir)
}

func (a *App) help() {
	txt := `[black::b]Teclas (también con las letras indicadas)[black::-]

 F1  ?   Esta ayuda            F2  c   Configuración del servidor
 F3  o   Opciones DHCP         F4  e   Editar cliente / Intro
 F5  s   Iniciar / Parar       F6  r   Reservar la IP actual
 F7  b   Bloquear / permitir   F8  f   Olvidar lease
 Ins n   Nuevo cliente         Del     Eliminar perfil
 F9      Arranque con sistema  F10     Menú (Alt+letra)\n Alt-X q Salir   (con -no-server F5/F9 controlan systemd)

[black::b]Perfiles de cliente[black::-]
 Seleccione un cliente y pulse Intro: puede reservarle una
 IP, darle nombre, otro lease y cambiar sus DNS, router,
 dominio, NTP u opciones personalizadas (código:tipo:valor,
 tipos: string, ip, ips, u8, u16, u32, hex).
 Lo que deje vacío hereda la configuración global.

[black::b]Lease[black::-]  6h por defecto. Formatos: 6h, 90m, 1d12h, 3600.`
	tv := tview.NewTextView().SetDynamicColors(true).SetText(txt).SetTextColor(tvBlack)
	tv.SetBackgroundColor(tvGrey).SetBorder(true).SetBorderColor(tvWhite).SetTitle(" Ayuda ").SetTitleColor(tvBlack)
	tv.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEscape || ev.Key() == tcell.KeyEnter || ev.Key() == tcell.KeyF1 {
			a.popModal()
			return nil
		}
		return ev
	})
	a.pushModal(a.nextName("help"), tv, 66, 22)
}

// ------------------------------------------------------------------ acciones

func (a *App) toggleServer() {
	if !a.core.ServerMode {
		st := a.core.ServiceState()
		act := "start"
		if st.Active {
			act = "stop"
		}
		if err := a.core.ServiceCtl(act); err != nil {
			a.message("Servicio", err.Error())
		}
		a.refresh()
		return
	}
	if a.core.Srv.Running() {
		a.core.Stop()
	} else if err := a.core.Start(); err != nil {
		a.message("No se pudo iniciar", err.Error())
	}
	a.refresh()
}

// toggleBoot activa/desactiva el servicio al arrancar el sistema.
func (a *App) toggleBoot() {
	if a.core.ServerMode {
		a.message("Arranque del sistema", "Esta instancia lleva el servidor integrado.\nPara arrancar con el sistema use el servicio systemd\n(-headless) y gestiónelo con: dhcpd-tui -no-server")
		return
	}
	act := "enable"
	if a.core.ServiceState().Enabled {
		act = "disable"
	}
	if err := a.core.ServiceCtl(act); err != nil {
		a.message("Servicio", err.Error())
	}
	a.refresh()
}

func (a *App) reload() {
	if err := a.core.Reload(); err != nil {
		a.message("Error", err.Error())
	}
}

func (a *App) selected() *row {
	r, _ := a.table.GetSelection()
	if r < 1 || r > len(a.rows) {
		return nil
	}
	return &a.rows[r-1]
}

func (a *App) editSelected() {
	if r := a.selected(); r != nil {
		a.clientDialog(r.mac, false)
	}
}

func (a *App) apply(cfg *config.Config) bool {
	if err := a.core.Apply(cfg); err != nil {
		a.message("Configuración no válida", err.Error())
		return false
	}
	a.refresh()
	return true
}

func (a *App) reserveCurrent() {
	r := a.selected()
	if r == nil {
		return
	}
	if r.lease == nil && (r.cl == nil || r.cl.IP == "") {
		a.message("Reservar IP", "Este cliente no tiene ninguna IP asignada todavía.")
		return
	}
	cfg := a.core.Config()
	cl := cfg.FindClient(r.mac)
	if cl == nil {
		cfg.UpsertClient(config.Client{MAC: r.mac, Name: r.name})
		cl = cfg.FindClient(r.mac)
	}
	if r.lease != nil {
		cl.IP = r.lease.IP
	}
	if a.apply(cfg) {
		a.core.Log.Logf("Reservada %s para %s", cl.IP, r.mac)
	}
}

func (a *App) toggleBlock() {
	r := a.selected()
	if r == nil {
		return
	}
	cfg := a.core.Config()
	cl := cfg.FindClient(r.mac)
	if cl == nil {
		cfg.UpsertClient(config.Client{MAC: r.mac, Name: r.name})
		cl = cfg.FindClient(r.mac)
	}
	cl.Deny = !cl.Deny
	if a.apply(cfg) {
		a.core.Log.Logf("Cliente %s %s", r.mac, map[bool]string{true: "BLOQUEADO", false: "permitido"}[cl.Deny])
	}
}

func (a *App) forgetLease() {
	r := a.selected()
	if r == nil || r.lease == nil {
		return
	}
	mac := r.mac
	a.confirm("Olvidar lease", "¿Eliminar la concesión de "+r.ip+" ("+mac+")?", func() {
		a.core.ForgetLease(mac)
		a.core.Log.Logf("Lease de %s eliminado", mac)
		a.refresh()
	})
}

func (a *App) deleteProfile() {
	r := a.selected()
	if r == nil || r.cl == nil {
		return
	}
	mac := r.mac
	a.confirm("Eliminar perfil", "¿Eliminar la configuración individual de "+mac+"?\n(Se pierde su reserva y opciones propias.)", func() {
		cfg := a.core.Config()
		cfg.DeleteClient(mac)
		a.apply(cfg)
	})
}

// ------------------------------------------------------------------ refresco

func fmtDur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return config.Duration(d.Round(time.Minute)).String()
}

func profileTags(cl *config.Client) string {
	if cl == nil {
		return ""
	}
	var t []string
	if cl.IP != "" {
		t = append(t, "IP fija")
	}
	if cl.LeaseTime > 0 {
		t = append(t, "lease "+cl.LeaseTime.String())
	}
	o := cl.Options
	if len(o.DNS) > 0 {
		t = append(t, "DNS")
	}
	if len(o.Routers) > 0 {
		t = append(t, "GW")
	}
	if o.DomainName != "" {
		t = append(t, "dominio")
	}
	if len(o.NTP) > 0 {
		t = append(t, "NTP")
	}
	if len(o.Custom) > 0 {
		t = append(t, "opc")
	}
	if len(t) == 0 {
		return "perfil"
	}
	return strings.Join(t, ", ")
}

func (a *App) buildRows(cfg *config.Config, leases []dhcp.Lease) {
	a.rows = a.rows[:0]
	seen := map[string]bool{}
	for i := range leases {
		l := leases[i]
		seen[l.MAC] = true
		a.rows = append(a.rows, row{mac: l.MAC, ip: l.IP, name: l.Hostname, vendor: l.Vendor, lease: &l, cl: cfg.FindClient(l.MAC)})
	}
	for i := range cfg.Clients {
		cl := &cfg.Clients[i]
		if !seen[cl.MAC] {
			a.rows = append(a.rows, row{mac: cl.MAC, ip: cl.IP, name: cl.Name, cl: cl})
		}
	}
	key := func(r row) uint64 {
		if ip := config.ParseIPv4(r.ip); ip != nil {
			return uint64(config.IPToUint(ip))
		}
		return 1 << 40
	}
	sort.SliceStable(a.rows, func(i, j int) bool {
		ki, kj := key(a.rows[i]), key(a.rows[j])
		if ki != kj {
			return ki < kj
		}
		return a.rows[i].mac < a.rows[j].mac
	})
}

func (a *App) refresh() {
	now := time.Now()
	cfg := a.core.Config()
	a.buildRows(cfg, a.core.DB.Snapshot())

	prev := ""
	if r := a.selected(); r != nil {
		prev = r.mac
	}
	selRow := 1
	a.table.Clear()
	hdr := []string{"Estado", "Dirección IP", "MAC", "Nombre", "Expira", "Configuración"}
	for c, h := range hdr {
		a.table.SetCell(0, c, tview.NewTableCell(" "+h).SetTextColor(tvYellow).SetSelectable(false).SetAttributes(tcell.AttrBold).SetExpansion(map[bool]int{true: 1}[c == 3 || c == 5]))
	}
	active := 0
	for i, r := range a.rows {
		state, color := "Reserva", tcell.ColorAqua
		exp := "-"
		switch {
		case r.cl != nil && r.cl.Deny:
			state, color = "Bloqueado", tcell.ColorRed
		case r.lease != nil && r.lease.Active(now):
			state, color = "Activo", tcell.ColorLime
		case r.lease != nil:
			state, color = "Expirado", tvGrey
		case r.cl != nil && r.cl.IP == "":
			state = "Perfil"
		}
		if r.lease != nil {
			if r.lease.Active(now) {
				active++
				exp = fmtDur(r.lease.Expiry.Sub(now))
			} else {
				exp = "hace " + fmtDur(now.Sub(r.lease.Expiry))
			}
		}
		name := r.name
		if r.cl != nil && r.cl.Name != "" {
			name = r.cl.Name
		}
		ip := r.ip
		if ip == "" {
			ip = "-"
		}
		cells := []string{state, ip, r.mac, name, exp, profileTags(r.cl)}
		for c, txt := range cells {
			cell := tview.NewTableCell(" " + txt).SetTextColor(color)
			if c == 3 || c == 5 {
				cell.SetExpansion(1)
			}
			a.table.SetCell(i+1, c, cell)
		}
		if r.mac == prev {
			selRow = i + 1
		}
	}
	if len(a.rows) > 0 {
		a.table.Select(selRow, 0)
	}
	a.table.SetTitle(fmt.Sprintf(" Clientes (%d activos / %d) ", active, len(a.rows)))
	if len(a.rows) == 0 {
		a.table.SetCell(1, 3, tview.NewTableCell("Sin clientes todavía — pulse Ins para añadir uno").SetTextColor(tvGrey).SetSelectable(false))
	}

	a.drawStatus(cfg, active, now)
	lines, ver := a.core.Log.Tail(60)
	if ver != a.logVer {
		a.logVer = ver
		var sb strings.Builder
		for _, l := range lines {
			ts, msg := l, ""
			if len(l) > 9 {
				ts, msg = l[:8], l[9:]
			}
			col := "white"
			switch {
			case strings.Contains(msg, "NAK") || strings.Contains(msg, "ERROR") || strings.Contains(msg, "Error"):
				col = "red"
			case strings.Contains(msg, "ACK"):
				col = "lime"
			case strings.Contains(msg, "OFFER"):
				col = "aqua"
			}
			fmt.Fprintf(&sb, "[silver]%s [%s]%s\n", ts, col, tview.Escape(msg))
		}
		a.logv.SetText(sb.String())
		a.logv.ScrollToEnd()
	}
}

func (a *App) drawStatus(cfg *config.Config, active int, now time.Time) {
	srv := a.core.Srv
	state := "[red::b]○ DETENIDO"
	switch {
	case !a.core.ServerMode:
		st := a.core.ServiceState()
		boot := "no"
		if st.Enabled {
			boot = "sí"
		}
		switch {
		case !st.Available:
			state = "[yellow::b]◐ SOLO CONFIG. (sin systemd)"
		case st.Active:
			state = "[lime::b]● SERVICIO ACTIVO[-::-] [silver]arranque: [white]" + boot
		default:
			state = "[red::b]○ SERVICIO PARADO[-::-] [silver]arranque: [white]" + boot
		}
	case srv.Running():
		state = "[lime::b]● ACTIVO"
	case a.core.LastError != "":
		state = "[red::b]○ DETENIDO (ver registro)"
	}
	sip := "?"
	if ip, err := dhcp.ResolveServerIP(cfg); err == nil {
		sip = ip.String()
	}
	pool := int(config.IPToUint(config.ParseIPv4(cfg.RangeEnd))-config.IPToUint(config.ParseIPv4(cfg.RangeStart))) + 1
	st := &srv.Stats
	var sb strings.Builder
	fmt.Fprintf(&sb, "[silver]Estado:[white] %s[-::-]   [silver]Interfaz:[white] %s (%s)   [silver]Subred:[white] %s\n", state, cfg.Interface, sip, cfg.Subnet)
	fmt.Fprintf(&sb, "[silver]Rango :[white] %s - %s (%d)   [silver]Lease:[white] %s   [silver]Reservas:[white] %d\n",
		cfg.RangeStart, cfg.RangeEnd, pool, cfg.LeaseTime, countReserved(cfg))
	fmt.Fprintf(&sb, "[silver]Activos:[white] %d   [silver]DISCOVER[white] %d  [silver]OFFER[white] %d  [silver]REQUEST[white] %d  [silver]ACK[white] %d  [silver]NAK[white] %d",
		active, st.Discover.Load(), st.Offer.Load(), st.Request.Load(), st.Ack.Load(), st.Nak.Load())
	a.status.SetText(sb.String())
}

func countReserved(cfg *config.Config) int {
	n := 0
	for _, c := range cfg.Clients {
		if c.IP != "" {
			n++
		}
	}
	return n
}
