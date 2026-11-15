package config

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const DefaultLeaseTime = 6 * time.Hour

// Client es la configuración individual de un cliente (identificado por MAC).
type Client struct {
	MAC       string   `json:"mac"`
	Name      string   `json:"name,omitempty"` // nombre de host asignado (opción 12)
	IP        string   `json:"ip,omitempty"`   // reserva de IP
	LeaseTime Duration `json:"lease_time,omitempty"`
	Deny      bool     `json:"deny,omitempty"` // ignorar a este cliente
	Options   Options  `json:"options,omitempty"`
}

// Config es la configuración completa del servidor.
type Config struct {
	Autostart     bool     `json:"autostart"`
	Interface     string   `json:"interface"`
	ServerIP      string   `json:"server_ip,omitempty"` // auto: IP de la interfaz
	Subnet        string   `json:"subnet"`              // CIDR
	RangeStart    string   `json:"range_start"`
	RangeEnd      string   `json:"range_end"`
	Exclude       []string `json:"exclude,omitempty"` // "ip" o "ip-ip"
	LeaseTime     Duration `json:"lease_time"`
	RenewalTime   Duration `json:"renewal_time,omitempty"`   // T1 (auto: 50%)
	RebindingTime Duration `json:"rebinding_time,omitempty"` // T2 (auto: 87,5%)
	Authoritative bool     `json:"authoritative"`
	DeclineHold   Duration `json:"decline_hold"`
	Options       Options  `json:"options"`
	Clients       []Client `json:"clients"`
}

// IfaceInfo describe una interfaz utilizable.
type IfaceInfo struct {
	Name string
	IP   net.IP
	Net  *net.IPNet
}

// Interfaces lista las interfaces activas con IPv4 (sin loopback).
func Interfaces() []IfaceInfo {
	var out []IfaceInfo
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				out = append(out, IfaceInfo{Name: ifc.Name, IP: n.IP.To4(), Net: &net.IPNet{IP: n.IP.Mask(n.Mask).To4(), Mask: n.Mask}})
				break
			}
		}
	}
	return out
}

// Default genera una configuración inicial razonable.
func Default() *Config {
	c := &Config{
		Autostart:     true,
		Interface:     "eth0",
		Subnet:        "192.168.1.0/24",
		RangeStart:    "192.168.1.100",
		RangeEnd:      "192.168.1.199",
		LeaseTime:     Duration(DefaultLeaseTime),
		Authoritative: true,
		DeclineHold:   Duration(10 * time.Minute),
		Options:       Options{Routers: []string{"192.168.1.1"}, DNS: []string{"1.1.1.1", "8.8.8.8"}},
	}
	if ifs := Interfaces(); len(ifs) > 0 {
		c.ApplyInterface(ifs[0])
	}
	return c
}

// ApplyInterface ajusta subred, rango y router a la interfaz indicada.
func (c *Config) ApplyInterface(i IfaceInfo) {
	c.Interface = i.Name
	c.ServerIP = ""
	c.Subnet = i.Net.String()
	base := IPToUint(i.Net.IP)
	ones, bits := i.Net.Mask.Size()
	size := uint32(1) << uint(bits-ones)
	if size >= 256 {
		c.RangeStart = UintToIP(base + 100).String()
		c.RangeEnd = UintToIP(base + 199).String()
	} else if size >= 8 {
		c.RangeStart = UintToIP(base + 2).String()
		c.RangeEnd = UintToIP(base + size - 2).String()
	}
	c.Options.Routers = []string{i.IP.String()}
}

func IPToUint(ip net.IP) uint32 { return binary.BigEndian.Uint32(ip.To4()) }
func UintToIP(n uint32) net.IP {
	b := make(net.IP, 4)
	binary.BigEndian.PutUint32(b, n)
	return b
}

// Network devuelve la subred parseada.
func (c *Config) Network() (*net.IPNet, error) {
	ip, n, err := net.ParseCIDR(strings.TrimSpace(c.Subnet))
	if err != nil || ip.To4() == nil {
		return nil, fmt.Errorf("subred no válida %q (use CIDR, p.ej. 192.168.1.0/24)", c.Subnet)
	}
	n.IP = n.IP.To4()
	return n, nil
}

// ExcludeRanges devuelve los rangos excluidos como pares [lo,hi].
func (c *Config) ExcludeRanges() ([][2]uint32, error) {
	var out [][2]uint32
	for _, e := range c.Exclude {
		lo, hi, ok := strings.Cut(e, "-")
		a := ParseIPv4(lo)
		b := a
		if ok {
			b = ParseIPv4(hi)
		}
		if a == nil || b == nil || IPToUint(a) > IPToUint(b) {
			return nil, fmt.Errorf("exclusión no válida %q (use ip o ip-ip)", e)
		}
		out = append(out, [2]uint32{IPToUint(a), IPToUint(b)})
	}
	return out, nil
}

// NormMAC normaliza una MAC a minúsculas con ':'.
func NormMAC(s string) (string, error) {
	hw, err := net.ParseMAC(strings.TrimSpace(s))
	if err != nil {
		return "", fmt.Errorf("MAC no válida %q", s)
	}
	return hw.String(), nil
}

// Validate comprueba la coherencia de toda la configuración.
func (c *Config) Validate() error {
	n, err := c.Network()
	if err != nil {
		return err
	}
	rs, re := ParseIPv4(c.RangeStart), ParseIPv4(c.RangeEnd)
	if rs == nil || re == nil {
		return errors.New("rango de direcciones no válido")
	}
	if !n.Contains(rs) || !n.Contains(re) {
		return errors.New("el rango debe estar dentro de la subred")
	}
	if IPToUint(rs) > IPToUint(re) {
		return errors.New("el inicio del rango es mayor que el final")
	}
	if c.ServerIP != "" {
		if sip := ParseIPv4(c.ServerIP); sip == nil || !n.Contains(sip) {
			return errors.New("la IP del servidor no es válida o no está en la subred")
		}
	}
	if c.LeaseTime.D() < time.Minute {
		return errors.New("el tiempo de concesión (lease) debe ser de al menos 1 minuto")
	}
	if c.RenewalTime > 0 && c.RebindingTime > 0 && c.RenewalTime >= c.RebindingTime {
		return errors.New("T1 (renovación) debe ser menor que T2 (reenlace)")
	}
	if _, err := c.ExcludeRanges(); err != nil {
		return err
	}
	if err := c.Options.Validate(); err != nil {
		return err
	}
	macs, ips := map[string]bool{}, map[string]string{}
	for i := range c.Clients {
		cl := &c.Clients[i]
		m, err := NormMAC(cl.MAC)
		if err != nil {
			return err
		}
		cl.MAC = m
		if macs[m] {
			return fmt.Errorf("MAC duplicada %s", m)
		}
		macs[m] = true
		if cl.IP != "" {
			ip := ParseIPv4(cl.IP)
			if ip == nil || !n.Contains(ip) {
				return fmt.Errorf("cliente %s: la IP reservada %q no está en la subred", m, cl.IP)
			}
			if other, dup := ips[ip.String()]; dup {
				return fmt.Errorf("la IP %s está reservada a %s y a %s", ip, other, m)
			}
			ips[ip.String()] = m
		}
		if cl.LeaseTime != 0 && cl.LeaseTime.D() < time.Minute {
			return fmt.Errorf("cliente %s: lease demasiado corto", m)
		}
		if err := cl.Options.Validate(); err != nil {
			return fmt.Errorf("cliente %s: %w", m, err)
		}
	}
	return nil
}

// FindClient busca el perfil de un cliente por MAC normalizada.
func (c *Config) FindClient(mac string) *Client {
	for i := range c.Clients {
		if strings.EqualFold(c.Clients[i].MAC, mac) {
			return &c.Clients[i]
		}
	}
	return nil
}

// UpsertClient inserta o reemplaza el perfil (por MAC).
func (c *Config) UpsertClient(cl Client) {
	if old := c.FindClient(cl.MAC); old != nil {
		*old = cl
		return
	}
	c.Clients = append(c.Clients, cl)
	sort.Slice(c.Clients, func(i, j int) bool { return c.Clients[i].MAC < c.Clients[j].MAC })
}

// DeleteClient elimina el perfil; devuelve si existía.
func (c *Config) DeleteClient(mac string) bool {
	for i := range c.Clients {
		if strings.EqualFold(c.Clients[i].MAC, mac) {
			c.Clients = append(c.Clients[:i], c.Clients[i+1:]...)
			return true
		}
	}
	return false
}

// Clone hace una copia profunda.
func (c *Config) Clone() *Config {
	b, _ := json.Marshal(c)
	var n Config
	_ = json.Unmarshal(b, &n)
	return &n
}

// Load lee la configuración; si no existe devuelve Default y created=true.
func Load(path string) (cfg *Config, created bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), true, nil
	}
	if err != nil {
		return nil, false, err
	}
	cfg = &Config{}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, false, nil
}

// Save escribe de forma atómica.
func Save(path string, c *Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, b)
}

// WriteFileAtomic escribe en un temporal y renombra (seguro ante cortes de luz).
func WriteFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tmp.Name(), 0o644)
	return os.Rename(tmp.Name(), path)
}
