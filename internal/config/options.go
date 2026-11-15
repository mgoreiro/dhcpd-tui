package config

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Route es una ruta estática (opción 121, RFC 3442).
type Route struct {
	Dest    string `json:"dest"`    // CIDR, p.ej. 10.0.0.0/8
	Gateway string `json:"gateway"` // IPv4
}

// CustomOption es una opción DHCP arbitraria.
type CustomOption struct {
	Code  int    `json:"code"`
	Type  string `json:"type"` // string, ip, ips, u8, u16, u32, hex
	Value string `json:"value"`
}

// Options agrupa las opciones DHCP que se pueden fijar globalmente o por cliente.
type Options struct {
	Routers      []string       `json:"routers,omitempty"`       // 3
	DNS          []string       `json:"dns,omitempty"`           // 6
	DomainName   string         `json:"domain_name,omitempty"`   // 15
	DomainSearch []string       `json:"domain_search,omitempty"` // 119
	NTP          []string       `json:"ntp,omitempty"`           // 42
	WINS         []string       `json:"wins,omitempty"`          // 44
	TimeOffset   *int           `json:"time_offset,omitempty"`   // 2 (segundos)
	MTU          int            `json:"mtu,omitempty"`           // 26
	Broadcast    string         `json:"broadcast,omitempty"`     // 28 (auto si vacío)
	TFTPServer   string         `json:"tftp_server,omitempty"`   // 66
	BootFile     string         `json:"boot_file,omitempty"`     // 67
	NextServer   string         `json:"next_server,omitempty"`   // siaddr
	Routes       []Route        `json:"routes,omitempty"`        // 121 (+33)
	Custom       []CustomOption `json:"custom,omitempty"`
}

// Merge devuelve o con los campos no vacíos de over aplicados encima.
func (o Options) Merge(over Options) Options {
	r := o
	if len(over.Routers) > 0 {
		r.Routers = over.Routers
	}
	if len(over.DNS) > 0 {
		r.DNS = over.DNS
	}
	if over.DomainName != "" {
		r.DomainName = over.DomainName
	}
	if len(over.DomainSearch) > 0 {
		r.DomainSearch = over.DomainSearch
	}
	if len(over.NTP) > 0 {
		r.NTP = over.NTP
	}
	if len(over.WINS) > 0 {
		r.WINS = over.WINS
	}
	if over.TimeOffset != nil {
		r.TimeOffset = over.TimeOffset
	}
	if over.MTU != 0 {
		r.MTU = over.MTU
	}
	if over.Broadcast != "" {
		r.Broadcast = over.Broadcast
	}
	if over.TFTPServer != "" {
		r.TFTPServer = over.TFTPServer
	}
	if over.BootFile != "" {
		r.BootFile = over.BootFile
	}
	if over.NextServer != "" {
		r.NextServer = over.NextServer
	}
	if len(over.Routes) > 0 {
		r.Routes = over.Routes
	}
	if len(over.Custom) > 0 {
		merged := append([]CustomOption(nil), o.Custom...)
		for _, c := range over.Custom {
			replaced := false
			for i := range merged {
				if merged[i].Code == c.Code {
					merged[i], replaced = c, true
				}
			}
			if !replaced {
				merged = append(merged, c)
			}
		}
		r.Custom = merged
	}
	return r
}

// IsZero indica si no hay ninguna opción definida.
func (o Options) IsZero() bool {
	return len(o.Routers) == 0 && len(o.DNS) == 0 && o.DomainName == "" &&
		len(o.DomainSearch) == 0 && len(o.NTP) == 0 && len(o.WINS) == 0 &&
		o.TimeOffset == nil && o.MTU == 0 && o.Broadcast == "" &&
		o.TFTPServer == "" && o.BootFile == "" && o.NextServer == "" &&
		len(o.Routes) == 0 && len(o.Custom) == 0
}

// Validate comprueba que todos los valores sean correctos.
func (o Options) Validate() error {
	for name, l := range map[string][]string{"routers": o.Routers, "DNS": o.DNS, "NTP": o.NTP, "WINS": o.WINS} {
		for _, s := range l {
			if ParseIPv4(s) == nil {
				return fmt.Errorf("%s: dirección IPv4 no válida %q", name, s)
			}
		}
	}
	for _, s := range []string{o.Broadcast, o.NextServer} {
		if s != "" && ParseIPv4(s) == nil {
			return fmt.Errorf("dirección IPv4 no válida %q", s)
		}
	}
	if o.MTU != 0 && (o.MTU < 68 || o.MTU > 65535) {
		return fmt.Errorf("MTU fuera de rango (68-65535)")
	}
	for _, r := range o.Routes {
		if _, _, err := net.ParseCIDR(r.Dest); err != nil || strings.Contains(r.Dest, ":") {
			return fmt.Errorf("ruta: destino CIDR no válido %q", r.Dest)
		}
		if ParseIPv4(r.Gateway) == nil {
			return fmt.Errorf("ruta: gateway no válido %q", r.Gateway)
		}
	}
	for _, c := range o.Custom {
		switch c.Code {
		case 0, 51, 52, 53, 54, 55, 255:
			return fmt.Errorf("opción personalizada %d: código reservado por el servidor", c.Code)
		}
		if _, err := c.Encode(); err != nil {
			return err
		}
	}
	return nil
}

// ParseIPv4 devuelve la IP de 4 bytes o nil.
func ParseIPv4(s string) net.IP {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return nil
	}
	return ip.To4()
}

// Encode serializa el valor según su tipo.
func (c CustomOption) Encode() ([]byte, error) {
	if c.Code < 1 || c.Code > 254 {
		return nil, fmt.Errorf("opción personalizada: código %d fuera de rango (1-254)", c.Code)
	}
	bad := func(err error) ([]byte, error) {
		return nil, fmt.Errorf("opción %d (%s): valor %q no válido: %v", c.Code, c.Type, c.Value, err)
	}
	switch strings.ToLower(c.Type) {
	case "string", "str":
		return []byte(c.Value), nil
	case "ip":
		ip := ParseIPv4(c.Value)
		if ip == nil {
			return bad(fmt.Errorf("no es IPv4"))
		}
		return []byte(ip), nil
	case "ips":
		var out []byte
		for _, s := range SplitList(c.Value) {
			ip := ParseIPv4(s)
			if ip == nil {
				return bad(fmt.Errorf("%q no es IPv4", s))
			}
			out = append(out, ip...)
		}
		return out, nil
	case "u8", "u16", "u32":
		bits := map[string]int{"u8": 8, "u16": 16, "u32": 32}[strings.ToLower(c.Type)]
		n, err := strconv.ParseUint(strings.TrimSpace(c.Value), 0, bits)
		if err != nil {
			return bad(err)
		}
		switch bits {
		case 8:
			return []byte{byte(n)}, nil
		case 16:
			return binary.BigEndian.AppendUint16(nil, uint16(n)), nil
		}
		return binary.BigEndian.AppendUint32(nil, uint32(n)), nil
	case "hex":
		clean := strings.NewReplacer(":", "", "-", "", " ", "").Replace(c.Value)
		b, err := hex.DecodeString(clean)
		if err != nil {
			return bad(err)
		}
		return b, nil
	}
	return nil, fmt.Errorf("opción %d: tipo desconocido %q (string, ip, ips, u8, u16, u32, hex)", c.Code, c.Type)
}

// SplitList separa por comas, punto y coma o espacios.
func SplitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
}

func JoinList(l []string) string { return strings.Join(l, ", ") }

// ParseRoutes interpreta "10.0.0.0/8>192.168.1.1; 172.16.0.0/12>192.168.1.2".
func ParseRoutes(s string) ([]Route, error) {
	var out []Route
	for _, part := range SplitList(s) {
		d, g, ok := strings.Cut(part, ">")
		if !ok {
			return nil, fmt.Errorf("ruta %q: use destino/CIDR>gateway", part)
		}
		out = append(out, Route{Dest: strings.TrimSpace(d), Gateway: strings.TrimSpace(g)})
	}
	return out, nil
}

func FormatRoutes(r []Route) string {
	p := make([]string, len(r))
	for i, x := range r {
		p[i] = x.Dest + ">" + x.Gateway
	}
	return strings.Join(p, "; ")
}

// ParseCustom interpreta "66:string:servidor; 150:ip:10.0.0.5".
func ParseCustom(s string) ([]CustomOption, error) {
	var out []CustomOption
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		f := strings.SplitN(part, ":", 3)
		if len(f) != 3 {
			return nil, fmt.Errorf("opción %q: use código:tipo:valor", part)
		}
		code, err := strconv.Atoi(strings.TrimSpace(f[0]))
		if err != nil {
			return nil, fmt.Errorf("opción %q: código no numérico", part)
		}
		out = append(out, CustomOption{Code: code, Type: strings.TrimSpace(f[1]), Value: strings.TrimSpace(f[2])})
	}
	return out, nil
}

func FormatCustom(c []CustomOption) string {
	p := make([]string, len(c))
	for i, x := range c {
		p[i] = fmt.Sprintf("%d:%s:%s", x.Code, x.Type, x.Value)
	}
	return strings.Join(p, "; ")
}
