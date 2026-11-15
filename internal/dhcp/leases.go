package dhcp

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"sync"
	"time"

	"dhcpd-tui/internal/config"
)

// Lease es una concesión de dirección a un cliente.
type Lease struct {
	MAC      string    `json:"mac"`
	IP       string    `json:"ip"`
	Hostname string    `json:"hostname,omitempty"`
	Vendor   string    `json:"vendor,omitempty"`
	Start    time.Time `json:"start"`
	Expiry   time.Time `json:"expiry"`
}

func (l Lease) Active(now time.Time) bool { return now.Before(l.Expiry) }

type offer struct {
	ip     string
	expiry time.Time
}

// retención del historial de leases expirados
const keepExpired = 30 * 24 * time.Hour

// DB guarda las concesiones, las ofertas pendientes y las IP rechazadas.
type DB struct {
	mu       sync.Mutex
	path     string
	byMAC    map[string]*Lease
	offers   map[string]offer
	declined map[string]time.Time
	dirty    bool
	stop     chan struct{}
	modTime  time.Time
}

func NewDB(path string) *DB {
	return &DB{
		path: path, byMAC: map[string]*Lease{}, offers: map[string]offer{},
		declined: map[string]time.Time{}, stop: make(chan struct{}),
	}
}

// Load lee el fichero de leases (si existe).
func (d *DB) Load() error {
	if d.path == "" {
		return nil
	}
	st, err := os.Stat(d.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	b, err := os.ReadFile(d.path)
	if err != nil {
		return err
	}
	var ls []Lease
	if err := json.Unmarshal(b, &ls); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.byMAC = map[string]*Lease{}
	for i := range ls {
		l := ls[i]
		d.byMAC[l.MAC] = &l
	}
	d.modTime = st.ModTime()
	return nil
}

// Reload relee el fichero solo si ha cambiado en disco (modo solo-TUI).
func (d *DB) Reload() bool {
	st, err := os.Stat(d.path)
	if err != nil {
		return false
	}
	d.mu.Lock()
	same := st.ModTime().Equal(d.modTime)
	d.mu.Unlock()
	if same {
		return false
	}
	return d.Load() == nil
}

func (d *DB) flushLocked() error {
	if d.path == "" || !d.dirty {
		return nil
	}
	ls := make([]Lease, 0, len(d.byMAC))
	for _, l := range d.byMAC {
		ls = append(ls, *l)
	}
	sort.Slice(ls, func(i, j int) bool { return ls[i].MAC < ls[j].MAC })
	b, _ := json.MarshalIndent(ls, "", "  ")
	if err := config.WriteFileAtomic(d.path, b); err != nil {
		return err
	}
	d.dirty = false
	if st, err := os.Stat(d.path); err == nil {
		d.modTime = st.ModTime()
	}
	return nil
}

// StartFlusher guarda los cambios cada 2 s (protege tarjetas SD de escrituras excesivas).
func (d *DB) StartFlusher() {
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				d.mu.Lock()
				_ = d.flushLocked()
				d.mu.Unlock()
			case <-d.stop:
				return
			}
		}
	}()
}

// Close detiene el flusher y guarda.
func (d *DB) Close() error {
	select {
	case <-d.stop:
	default:
		close(d.stop)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.flushLocked()
}

func (d *DB) Get(mac string) (Lease, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if l, ok := d.byMAC[mac]; ok {
		return *l, true
	}
	return Lease{}, false
}

// Snapshot devuelve todos los leases ordenados por IP.
func (d *DB) Snapshot() []Lease {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]Lease, 0, len(d.byMAC))
	for _, l := range d.byMAC {
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool {
		return config.IPToUint(config.ParseIPv4(out[i].IP)) < config.IPToUint(config.ParseIPv4(out[j].IP))
	})
	return out
}

// Commit registra (o renueva) una concesión. Retira cualquier otra concesión
// que tuviera la misma IP.
func (d *DB) Commit(l Lease) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for m, o := range d.byMAC {
		if o.IP == l.IP && m != l.MAC {
			delete(d.byMAC, m)
		}
	}
	d.byMAC[l.MAC] = &l
	delete(d.offers, l.MAC)
	d.dirty = true
}

func (d *DB) Delete(mac string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.byMAC[mac]; ok {
		delete(d.byMAC, mac)
		d.dirty = true
	}
}

// Prune elimina historial caducado hace mucho.
func (d *DB) Prune(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for m, l := range d.byMAC {
		if now.Sub(l.Expiry) > keepExpired {
			delete(d.byMAC, m)
			d.dirty = true
		}
	}
	for m, o := range d.offers {
		if now.After(o.expiry) {
			delete(d.offers, m)
		}
	}
	for ip, t := range d.declined {
		if now.After(t) {
			delete(d.declined, ip)
		}
	}
}

func (d *DB) SetOffer(mac, ip string, until time.Time) {
	d.mu.Lock()
	d.offers[mac] = offer{ip, until}
	d.mu.Unlock()
}

func (d *DB) DropOffer(mac string) {
	d.mu.Lock()
	delete(d.offers, mac)
	d.mu.Unlock()
}

func (d *DB) MarkDeclined(ip string, until time.Time) {
	d.mu.Lock()
	d.declined[ip] = until
	d.mu.Unlock()
}

// Busy indica si la IP está ocupada por alguien distinto de mac.
func (d *DB) Busy(ip, mac string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t, ok := d.declined[ip]; ok && now.Before(t) {
		return true
	}
	for m, l := range d.byMAC {
		if l.IP == ip && m != mac && l.Active(now) {
			return true
		}
	}
	for m, o := range d.offers {
		if o.ip == ip && m != mac && now.Before(o.expiry) {
			return true
		}
	}
	return false
}
