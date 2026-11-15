package tui

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"dhcpd-tui/internal/config"
)

func addInput(f *tview.Form, label, text string, width int) *tview.InputField {
	f.AddInputField(label, text, width, nil, nil)
	return f.GetFormItem(f.GetFormItemCount() - 1).(*tview.InputField)
}

func styleDropDown(d *tview.DropDown) {
	d.SetListStyles(tcell.StyleDefault.Foreground(tvWhite).Background(tvBlue),
		tcell.StyleDefault.Foreground(tvBlack).Background(tvGreen))
	d.SetPrefixStyle(tcell.StyleDefault.Foreground(tvYellow).Background(tvBlue))
}

func formSize(f *tview.Form, a *App, width int) (int, int) {
	h := f.GetFormItemCount() + 6 // marco, relleno, hueco, botones, sombra
	_, _, _, sh := a.pages.GetRect()
	if sh > 0 {
		h = min(h, sh-1)
	}
	return width, h
}

// showForm muestra un formulario modal; Esc cancela.
func (a *App) showForm(f *tview.Form, width int) {
	f.SetCancelFunc(a.popModal)
	w, h := formSize(f, a, width)
	a.pushModal(a.nextName("form"), f, w, h)
}

func parseOffset(s string) (*int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimLeft(s, "+-")
	if n, err := strconv.Atoi(s); err == nil {
		if neg {
			n = -n
		}
		return &n, nil
	}
	d, err := config.ParseDuration(s)
	if err != nil {
		return nil, err
	}
	n := int(d.D().Seconds())
	if neg {
		n = -n
	}
	return &n, nil
}

// ------------------------------------------------------------------ servidor

func (a *App) serverDialog() {
	cfg := a.core.Config()
	f := tview.NewForm()
	styleForm(f, "Configuración del servidor")

	ifs := config.Interfaces()
	byName := map[string]config.IfaceInfo{}
	var names []string
	cur := 0
	for _, i := range ifs {
		byName[i.Name] = i
		names = append(names, i.Name)
	}
	if _, ok := byName[cfg.Interface]; !ok {
		names = append(names, cfg.Interface)
	}
	for i, n := range names {
		if n == cfg.Interface {
			cur = i
		}
	}
	dd := tview.NewDropDown().SetLabel("Interfaz de red").SetOptions(names, nil).SetCurrentOption(cur)
	styleDropDown(dd)
	f.AddFormItem(dd)
	sip := addInput(f, "IP del servidor (vacío=auto)", cfg.ServerIP, 18)
	sub := addInput(f, "Subred (CIDR)", cfg.Subnet, 18)
	rs := addInput(f, "Rango: primera IP", cfg.RangeStart, 18)
	re := addInput(f, "Rango: última IP", cfg.RangeEnd, 18)
	ex := addInput(f, "Excluir (ip o ip-ip, ...)", strings.Join(cfg.Exclude, ", "), 30)
	lease := addInput(f, "Lease (6h, 90m, 1d...)", cfg.LeaseTime.String(), 12)
	gw := addInput(f, "Router(s)", config.JoinList(cfg.Options.Routers), 30)
	dns := addInput(f, "Servidores DNS", config.JoinList(cfg.Options.DNS), 30)
	dom := addInput(f, "Nombre de dominio", cfg.Options.DomainName, 30)
	f.AddCheckbox("Autoritativo (NAK a desconocidos)", cfg.Authoritative, nil)
	f.AddCheckbox("Iniciar servidor al arrancar", cfg.Autostart, nil)
	chkAuth := f.GetFormItem(f.GetFormItemCount() - 2).(*tview.Checkbox)
	chkAuto := f.GetFormItem(f.GetFormItemCount() - 1).(*tview.Checkbox)

	// Al elegir otra interfaz se proponen subred, rango y router.
	dd.SetSelectedFunc(func(text string, _ int) {
		if i, ok := byName[text]; ok && text != cfg.Interface {
			tmp := config.Config{}
			tmp.ApplyInterface(i)
			sip.SetText("")
			sub.SetText(tmp.Subnet)
			rs.SetText(tmp.RangeStart)
			re.SetText(tmp.RangeEnd)
			gw.SetText(config.JoinList(tmp.Options.Routers))
		}
	})

	f.AddButton("Aceptar", func() {
		n := cfg.Clone()
		_, n.Interface = dd.GetCurrentOption()
		n.ServerIP = strings.TrimSpace(sip.GetText())
		n.Subnet = strings.TrimSpace(sub.GetText())
		n.RangeStart = strings.TrimSpace(rs.GetText())
		n.RangeEnd = strings.TrimSpace(re.GetText())
		n.Exclude = config.SplitList(ex.GetText())
		lt, err := config.ParseDuration(lease.GetText())
		if err != nil {
			a.message("Lease no válido", err.Error())
			return
		}
		n.LeaseTime = lt
		n.Options.Routers = config.SplitList(gw.GetText())
		n.Options.DNS = config.SplitList(dns.GetText())
		n.Options.DomainName = strings.TrimSpace(dom.GetText())
		n.Authoritative = chkAuth.IsChecked()
		n.Autostart = chkAuto.IsChecked()
		if a.apply(n) {
			a.popModal()
			a.core.Log.Logf("Configuración del servidor guardada")
		}
	})
	f.AddButton("Cancelar", a.popModal)
	a.showForm(f, 72)
}

// ------------------------------------------------------------------ opciones avanzadas

func (a *App) optionsDialog() {
	cfg := a.core.Config()
	o := cfg.Options
	f := tview.NewForm()
	styleForm(f, "Opciones DHCP globales")

	ntp := addInput(f, "Servidores NTP (42)", config.JoinList(o.NTP), 34)
	wins := addInput(f, "Servidores WINS/NetBIOS (44)", config.JoinList(o.WINS), 34)
	srch := addInput(f, "Dominios de búsqueda (119)", config.JoinList(o.DomainSearch), 34)
	off := ""
	if o.TimeOffset != nil {
		off = strconv.Itoa(*o.TimeOffset)
	}
	toff := addInput(f, "Desfase horario seg. (2)", off, 10)
	mtu := addInput(f, "MTU de interfaz (26)", map[bool]string{true: strconv.Itoa(o.MTU)}[o.MTU > 0], 8)
	bc := addInput(f, "Broadcast (28, vacío=auto)", o.Broadcast, 18)
	tftp := addInput(f, "Servidor TFTP (66)", o.TFTPServer, 34)
	boot := addInput(f, "Fichero de arranque (67)", o.BootFile, 34)
	nxt := addInput(f, "Next-server (siaddr)", o.NextServer, 18)
	rts := addInput(f, "Rutas: red/CIDR>gw; ... (121)", config.FormatRoutes(o.Routes), 34)
	cus := addInput(f, "Otras: cód:tipo:valor; ...", config.FormatCustom(o.Custom), 34)
	t1 := addInput(f, "T1 renovación (vacío=50%)", map[bool]string{true: cfg.RenewalTime.String()}[cfg.RenewalTime > 0], 10)
	t2 := addInput(f, "T2 reenlace (vacío=87,5%)", map[bool]string{true: cfg.RebindingTime.String()}[cfg.RebindingTime > 0], 10)
	dh := addInput(f, "Bloqueo tras DECLINE", cfg.DeclineHold.String(), 10)

	f.AddButton("Aceptar", func() {
		n := cfg.Clone()
		no := &n.Options
		no.NTP, no.WINS, no.DomainSearch = config.SplitList(ntp.GetText()), config.SplitList(wins.GetText()), config.SplitList(srch.GetText())
		var err error
		if no.TimeOffset, err = parseOffset(toff.GetText()); err != nil {
			a.message("Desfase no válido", err.Error())
			return
		}
		no.MTU = 0
		if s := strings.TrimSpace(mtu.GetText()); s != "" {
			if no.MTU, err = strconv.Atoi(s); err != nil {
				a.message("MTU no válido", "Debe ser un número")
				return
			}
		}
		no.Broadcast, no.TFTPServer, no.BootFile, no.NextServer = strings.TrimSpace(bc.GetText()), strings.TrimSpace(tftp.GetText()), strings.TrimSpace(boot.GetText()), strings.TrimSpace(nxt.GetText())
		if no.Routes, err = config.ParseRoutes(rts.GetText()); err != nil {
			a.message("Rutas no válidas", err.Error())
			return
		}
		if no.Custom, err = config.ParseCustom(cus.GetText()); err != nil {
			a.message("Opciones no válidas", err.Error())
			return
		}
		for _, p := range []struct {
			in  *tview.InputField
			dst *config.Duration
		}{{t1, &n.RenewalTime}, {t2, &n.RebindingTime}, {dh, &n.DeclineHold}} {
			if *p.dst, err = config.ParseDuration(p.in.GetText()); err != nil {
				a.message("Tiempo no válido", err.Error())
				return
			}
		}
		if n.DeclineHold == 0 {
			n.DeclineHold = cfg.DeclineHold
		}
		if a.apply(n) {
			a.popModal()
			a.core.Log.Logf("Opciones DHCP guardadas")
		}
	})
	f.AddButton("Cancelar", a.popModal)
	a.showForm(f, 72)
}

// ------------------------------------------------------------------ cliente

// clientDialog edita el perfil de un cliente. Con isNew=true la MAC es editable.
func (a *App) clientDialog(mac string, isNew bool) {
	cfg := a.core.Config()
	var cl config.Client
	lease, haveLease := a.core.DB.Get(mac)
	existing := false
	if c := cfg.FindClient(mac); c != nil {
		cl, existing = *c, true
	} else {
		cl.MAC = mac
		if haveLease {
			cl.Name = lease.Hostname
		}
	}
	title := "Cliente " + mac
	if isNew {
		title = "Nuevo cliente"
	}
	f := tview.NewForm()
	styleForm(f, title)

	macF := addInput(f, "Dirección MAC", cl.MAC, 20)
	if !isNew {
		macF.SetDisabled(true)
	}
	name := addInput(f, "Nombre de host", cl.Name, 28)
	ip := addInput(f, "IP reservada (vacío=dinámica)", cl.IP, 18)
	lt := addInput(f, "Lease propio (vacío=global)", map[bool]string{true: cl.LeaseTime.String()}[cl.LeaseTime > 0], 10)
	gw := addInput(f, "Router(s) propio(s)", config.JoinList(cl.Options.Routers), 28)
	dns := addInput(f, "DNS propios", config.JoinList(cl.Options.DNS), 28)
	dom := addInput(f, "Dominio propio", cl.Options.DomainName, 28)
	ntp := addInput(f, "NTP propios", config.JoinList(cl.Options.NTP), 28)
	cus := addInput(f, "Otras: cód:tipo:valor; ...", config.FormatCustom(cl.Options.Custom), 28)
	f.AddCheckbox("Bloquear (ignorar a este cliente)", cl.Deny, nil)
	deny := f.GetFormItem(f.GetFormItemCount() - 1).(*tview.Checkbox)

	f.AddButton("Aceptar", func() {
		n := cfg.Clone()
		c := cl
		var err error
		if isNew {
			if c.MAC, err = config.NormMAC(macF.GetText()); err != nil {
				a.message("MAC no válida", err.Error())
				return
			}
			if n.FindClient(c.MAC) != nil {
				a.message("Cliente existente", "Ya existe un perfil para "+c.MAC)
				return
			}
		}
		c.Name = strings.TrimSpace(name.GetText())
		c.IP = strings.TrimSpace(ip.GetText())
		if c.LeaseTime, err = config.ParseDuration(lt.GetText()); err != nil {
			a.message("Lease no válido", err.Error())
			return
		}
		c.Options.Routers = config.SplitList(gw.GetText())
		c.Options.DNS = config.SplitList(dns.GetText())
		c.Options.DomainName = strings.TrimSpace(dom.GetText())
		c.Options.NTP = config.SplitList(ntp.GetText())
		if c.Options.Custom, err = config.ParseCustom(cus.GetText()); err != nil {
			a.message("Opciones no válidas", err.Error())
			return
		}
		c.Deny = deny.IsChecked()
		n.UpsertClient(c)
		if a.apply(n) {
			a.popModal()
			a.core.Log.Logf("Perfil de %s guardado", c.MAC)
		}
	})
	if haveLease {
		f.AddButton("IP actual", func() { ip.SetText(lease.IP) })
	}
	if existing {
		f.AddButton("Eliminar", func() {
			a.popModal()
			a.deleteProfileMAC(cl.MAC)
		})
	}
	f.AddButton("Cancelar", a.popModal)
	a.showForm(f, 70)
}

func (a *App) deleteProfileMAC(mac string) {
	a.confirm("Eliminar perfil", "¿Eliminar la configuración individual de "+mac+"?", func() {
		cfg := a.core.Config()
		cfg.DeleteClient(mac)
		a.apply(cfg)
	})
}
