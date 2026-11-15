// Package tui implementa la interfaz de texto con aspecto Turbo Vision.
package tui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Paleta Turbo Vision (16 colores ANSI: funciona en cualquier terminal).
const (
	tvBlue   = tcell.ColorNavy
	tvGrey   = tcell.ColorSilver
	tvCyan   = tcell.ColorTeal
	tvGreen  = tcell.ColorGreen
	tvBlack  = tcell.ColorBlack
	tvWhite  = tcell.ColorWhite
	tvYellow = tcell.ColorYellow
	tvRed    = tcell.ColorMaroon
	tvDark   = tcell.ColorGray
)

func applyTheme() {
	tview.Styles.PrimitiveBackgroundColor = tvBlue
	tview.Styles.ContrastBackgroundColor = tvCyan
	tview.Styles.MoreContrastBackgroundColor = tvGreen
	tview.Styles.BorderColor = tvWhite
	tview.Styles.TitleColor = tvWhite
	tview.Styles.GraphicsColor = tvWhite
	tview.Styles.PrimaryTextColor = tvWhite
	tview.Styles.SecondaryTextColor = tvYellow
	tview.Styles.TertiaryTextColor = tcell.ColorAqua
	tview.Styles.InverseTextColor = tvBlack
	tview.Styles.ContrastSecondaryTextColor = tvBlack

	// Marcos de línea doble, como las ventanas de Turbo Vision.
	tview.Borders.Horizontal, tview.Borders.HorizontalFocus = '═', '═'
	tview.Borders.Vertical, tview.Borders.VerticalFocus = '║', '║'
	tview.Borders.TopLeft, tview.Borders.TopLeftFocus = '╔', '╔'
	tview.Borders.TopRight, tview.Borders.TopRightFocus = '╗', '╗'
	tview.Borders.BottomLeft, tview.Borders.BottomLeftFocus = '╚', '╚'
	tview.Borders.BottomRight, tview.Borders.BottomRightFocus = '╝', '╝'
}

func drawText(s tcell.Screen, x, y, maxw int, text string, st tcell.Style) int {
	n := 0
	for _, r := range text {
		if n >= maxw {
			break
		}
		s.SetContent(x+n, y, r, nil, st)
		n++
	}
	return n
}

// ---------------------------------------------------------------- Desktop

// Desktop pinta el fondo ░ y reparte las ventanas.
type Desktop struct {
	*tview.Box
	status, clients, log tview.Primitive
	focus                tview.Primitive
}

func newDesktop(status, clients, log, focus tview.Primitive) *Desktop {
	return &Desktop{Box: tview.NewBox(), status: status, clients: clients, log: log, focus: focus}
}

func (d *Desktop) Draw(screen tcell.Screen) {
	x, y, w, h := d.GetRect()
	bg := tcell.StyleDefault.Foreground(tvGrey).Background(tvBlue)
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			screen.SetContent(xx, yy, '░', nil, bg)
		}
	}
	const statusH = 5
	logH := 7
	if h < 24 {
		logH = 5
	}
	cx, cw := x+1, w-2
	if w < 40 {
		cx, cw = x, w
	}
	clientsH := h - statusH - logH
	if clientsH < 5 {
		clientsH = 5
		logH = max(h-statusH-clientsH, 3)
	}
	d.status.SetRect(cx, y, cw, statusH)
	d.clients.SetRect(cx, y+statusH, cw, clientsH)
	d.log.SetRect(cx, y+statusH+clientsH, cw, logH)
	d.status.Draw(screen)
	d.clients.Draw(screen)
	d.log.Draw(screen)
}

func (d *Desktop) Focus(delegate func(p tview.Primitive)) { delegate(d.focus) }
func (d *Desktop) HasFocus() bool                         { return d.focus.HasFocus() }

func (d *Desktop) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return d.WrapInputHandler(func(ev *tcell.EventKey, setFocus func(tview.Primitive)) {
		if h := d.focus.InputHandler(); h != nil {
			h(ev, setFocus)
		}
	})
}

func (d *Desktop) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return d.WrapMouseHandler(func(a tview.MouseAction, ev *tcell.EventMouse, setFocus func(tview.Primitive)) (bool, tview.Primitive) {
		for _, p := range []tview.Primitive{d.clients, d.log, d.status} {
			mx, my := ev.Position()
			x, y, w, h := p.GetRect()
			if mx >= x && mx < x+w && my >= y && my < y+h {
				if mh := p.MouseHandler(); mh != nil {
					return mh(a, ev, setFocus)
				}
			}
		}
		return false, nil
	})
}

// ---------------------------------------------------------------- Overlay (diálogo con sombra)

// Overlay coloca inner centrado (o anclado) con sombra proyectada.
type Overlay struct {
	*tview.Box
	inner        tview.Primitive
	w, h         int
	anchored     bool
	ax, ay       int
	onOutsideClk func()
}

func newOverlay(inner tview.Primitive, w, h int) *Overlay {
	o := &Overlay{Box: tview.NewBox(), inner: inner, w: w, h: h}
	return o
}

func (o *Overlay) rect() (int, int, int, int) {
	x, y, w, h := o.GetRect()
	ww, hh := min(o.w+2, w), min(o.h+1, h) // +sombra
	if o.anchored {
		return min(o.ax, x+w-ww), o.ay, ww, min(hh, y+h-o.ay)
	}
	return x + (w-ww)/2, y + (h-hh)/2, ww, hh
}

func (o *Overlay) Draw(screen tcell.Screen) {
	x, y, w, h := o.rect()
	o.inner.SetRect(x, y, w-2, h-1)
	o.inner.Draw(screen)
	shade := func(xx, yy int) {
		r, comb, _, _ := screen.GetContent(xx, yy)
		screen.SetContent(xx, yy, r, comb, tcell.StyleDefault.Foreground(tvDark).Background(tvBlack))
	}
	for yy := y + 1; yy < y+h; yy++ {
		shade(x+w-2, yy)
		shade(x+w-1, yy)
	}
	for xx := x + 2; xx < x+w-2; xx++ {
		shade(xx, y+h-1)
	}
}

func (o *Overlay) Focus(delegate func(p tview.Primitive)) { delegate(o.inner) }
func (o *Overlay) HasFocus() bool                         { return o.inner.HasFocus() }

func (o *Overlay) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return o.WrapInputHandler(func(ev *tcell.EventKey, setFocus func(tview.Primitive)) {
		if h := o.inner.InputHandler(); h != nil {
			h(ev, setFocus)
		}
	})
}

func (o *Overlay) PasteHandler() func(string, func(tview.Primitive)) {
	return o.WrapPasteHandler(func(t string, setFocus func(tview.Primitive)) {
		if h := o.inner.PasteHandler(); h != nil {
			h(t, setFocus)
		}
	})
}

func (o *Overlay) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return o.WrapMouseHandler(func(a tview.MouseAction, ev *tcell.EventMouse, setFocus func(tview.Primitive)) (bool, tview.Primitive) {
		mx, my := ev.Position()
		x, y, w, h := o.inner.GetRect()
		if mx >= x && mx < x+w && my >= y && my < y+h {
			if mh := o.inner.MouseHandler(); mh != nil {
				return mh(a, ev, setFocus)
			}
			return true, nil
		}
		if a == tview.MouseLeftClick && o.onOutsideClk != nil {
			o.onOutsideClk()
			return true, nil
		}
		return true, nil // los diálogos son modales
	})
}

// ---------------------------------------------------------------- Barra de menús

type menuItem struct {
	label  string // con '&' antes de la tecla rápida
	key    string // texto de atajo mostrado
	action func()
	sep    bool
}

type menu struct {
	title string
	items []menuItem
}

func splitHot(label string) (before string, hot rune, after string) {
	i := strings.IndexByte(label, '&')
	if i < 0 || i+1 >= len(label) {
		return label, 0, ""
	}
	r := []rune(label[i+1:])
	return label[:i], r[0], string(r[1:])
}

// MenuBar es la barra superior.
type MenuBar struct {
	*tview.Box
	menus   []menu
	active  int // -1 = ninguno
	right   func() string
	pos     []int // x de cada menú (relativo)
	onClick func(i int)
}

func (m *MenuBar) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return m.WrapMouseHandler(func(a tview.MouseAction, ev *tcell.EventMouse, setFocus func(tview.Primitive)) (bool, tview.Primitive) {
		if a != tview.MouseLeftClick || m.onClick == nil {
			return false, nil
		}
		mx, _ := ev.Position()
		x, _, _, _ := m.GetRect()
		for i := len(m.pos) - 1; i >= 0; i-- {
			if mx-x >= m.pos[i] {
				m.onClick(i)
				return true, nil
			}
		}
		return false, nil
	})
}

func newMenuBar(menus []menu) *MenuBar {
	return &MenuBar{Box: tview.NewBox(), menus: menus, active: -1}
}

func (m *MenuBar) Draw(screen tcell.Screen) {
	x, y, w, _ := m.GetRect()
	norm := tcell.StyleDefault.Foreground(tvBlack).Background(tvGrey)
	hot := tcell.StyleDefault.Foreground(tvRed).Background(tvGrey)
	sel := tcell.StyleDefault.Foreground(tvWhite).Background(tvGreen)
	for i := 0; i < w; i++ {
		screen.SetContent(x+i, y, ' ', nil, norm)
	}
	m.pos = m.pos[:0]
	cx := x + 1
	drawText(screen, cx, y, 2, "≡ ", hot)
	cx += 3
	for i, mn := range m.menus {
		m.pos = append(m.pos, cx-x)
		st, ht := norm, hot
		if i == m.active {
			st, ht = sel, sel.Foreground(tvYellow)
		}
		b, h, a := splitHot(mn.title)
		cx += drawText(screen, cx, y, w, " "+b, st)
		if h != 0 {
			cx += drawText(screen, cx, y, w, string(h), ht)
		}
		cx += drawText(screen, cx, y, w, a+" ", st)
		cx++
	}
	if m.right != nil {
		t := m.right()
		drawText(screen, x+w-len([]rune(t))-1, y, len([]rune(t)), t, norm)
	}
}

// StatusBar es la línea inferior con atajos.
type StatusBar struct {
	*tview.Box
	keys [][2]string
}

func (s *StatusBar) Draw(screen tcell.Screen) {
	x, y, w, _ := s.GetRect()
	norm := tcell.StyleDefault.Foreground(tvBlack).Background(tvGrey)
	hot := tcell.StyleDefault.Foreground(tvRed).Background(tvGrey)
	for i := 0; i < w; i++ {
		screen.SetContent(x+i, y, ' ', nil, norm)
	}
	cx := x + 1
	for _, k := range s.keys {
		need := len([]rune(k[0])) + len([]rune(k[1])) + 3
		if cx+need > x+w {
			break
		}
		cx += drawText(screen, cx, y, w, k[0], hot)
		cx += drawText(screen, cx, y, w, k[1]+"  ", norm)
	}
}
