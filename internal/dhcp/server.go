package dhcp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dhcpd-tui/internal/config"
)

const offerHold = 2 * time.Minute

// derived guarda los valores calculados a partir de la configuración.
type derived struct {
	cfg      *config.Config
	network  *net.IPNet
	serverIP net.IP
	lo, hi   uint32
	excl     [][2]uint32
	reserved map[uint32]string // IP reservada -> MAC
}

// Stats son contadores de mensajes.
type Stats struct {
	Discover, Offer, Request, Ack, Nak, Decline, Release, Inform atomic.Int64
}

// Server es el servidor DHCP.
type Server struct {
	DB         *DB
	Stats      Stats
	Logf       func(format string, a ...any)
	ServerPort int
	ClientPort int

	d       atomic.Pointer[derived]
	mu      sync.Mutex // protege conn/running
	conn    *net.UDPConn
	running bool
}

func New(cfg *config.Config, db *DB, logf func(string, ...any)) (*Server, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{DB: db, Logf: logf, ServerPort: 67, ClientPort: 68}
	if err := s.SetConfig(cfg); err != nil {
		return nil, err
	}
	return s, nil
}

// SetConfig valida y aplica la configuración en caliente. Si la interfaz
// cambia con el servidor en marcha, reinicia el socket.
func (s *Server) SetConfig(cfg *config.Config) error {
	cfg = cfg.Clone()
	if err := cfg.Validate(); err != nil {
		return err
	}
	n, _ := cfg.Network()
	d := &derived{cfg: cfg, network: n, reserved: map[uint32]string{}}
	d.lo = config.IPToUint(config.ParseIPv4(cfg.RangeStart))
	d.hi = config.IPToUint(config.ParseIPv4(cfg.RangeEnd))
	d.excl, _ = cfg.ExcludeRanges()
	for _, c := range cfg.Clients {
		if c.IP != "" {
			d.reserved[config.IPToUint(config.ParseIPv4(c.IP))] = c.MAC
		}
	}
	d.serverIP, _ = ResolveServerIP(cfg)
	old := s.d.Swap(d)

	s.mu.Lock()
	restart := s.running && old != nil && old.cfg.Interface != cfg.Interface
	s.mu.Unlock()
	if restart {
		s.Stop()
		return s.Start()
	}
	return nil
}

// Config devuelve una copia de la configuración activa.
func (s *Server) Config() *config.Config { return s.d.Load().cfg.Clone() }

// ResolveServerIP determina la IP con la que se identifica el servidor.
func ResolveServerIP(cfg *config.Config) (net.IP, error) {
	n, err := cfg.Network()
	if err != nil {
		return nil, err
	}
	if cfg.ServerIP != "" {
		return config.ParseIPv4(cfg.ServerIP), nil
	}
	ifc, err := net.InterfaceByName(cfg.Interface)
	if err != nil {
		return nil, fmt.Errorf("interfaz %q no encontrada", cfg.Interface)
	}
	addrs, _ := ifc.Addrs()
	for _, a := range addrs {
		if in, ok := a.(*net.IPNet); ok && in.IP.To4() != nil && n.Contains(in.IP) {
			return in.IP.To4(), nil
		}
	}
	return nil, fmt.Errorf("la interfaz %s no tiene ninguna IP en %s (configure una IP estática o fije la IP del servidor)", cfg.Interface, cfg.Subnet)
}

func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Start abre el socket y atiende peticiones en segundo plano.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}
	d := s.d.Load()
	sip, err := ResolveServerIP(d.cfg)
	if err != nil {
		return err
	}
	nd := *d
	nd.serverIP = sip
	s.d.Store(&nd)

	conn, err := listenUDP(d.cfg.Interface, s.ServerPort)
	if err != nil {
		return fmt.Errorf("no se puede abrir UDP/%d en %s: %w (¿ejecuta como root?)", s.ServerPort, d.cfg.Interface, err)
	}
	s.conn, s.running = conn, true
	go s.serve(conn)
	go s.maintenance(conn)
	s.Logf("Servidor iniciado en %s (%s) rango %s - %s", d.cfg.Interface, sip, d.cfg.RangeStart, d.cfg.RangeEnd)
	return nil
}

// Stop detiene el servidor.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	s.conn.Close()
	s.Logf("Servidor detenido")
}

func (s *Server) maintenance(conn *net.UDPConn) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		s.mu.Lock()
		alive := s.running && s.conn == conn
		s.mu.Unlock()
		if !alive {
			return
		}
		s.DB.Prune(time.Now())
	}
}

func (s *Server) serve(conn *net.UDPConn) {
	buf := make([]byte, 4096)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			s.mu.Lock()
			alive := s.running && s.conn == conn
			s.mu.Unlock()
			if !alive || errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		req, err := Parse(buf[:n])
		if err != nil || req.Op != BootRequest || len(req.CHAddr) == 0 {
			continue
		}
		rep := s.Handle(req, time.Now())
		if rep == nil {
			continue
		}
		if _, err := conn.WriteToUDP(rep.Marshal(), s.destination(req, rep)); err != nil {
			s.Logf("Error enviando %s: %v", rep.MsgType(), err)
		}
	}
}

// destination decide a dónde enviar la respuesta (RFC 2131 §4.1).
func (s *Server) destination(req, rep *Packet) *net.UDPAddr {
	switch {
	case !isZeroIP(req.GIAddr):
		return &net.UDPAddr{IP: req.GIAddr, Port: s.ServerPort}
	case rep.MsgType() == Nak:
		return &net.UDPAddr{IP: net.IPv4bcast, Port: s.ClientPort}
	case !isZeroIP(req.CIAddr):
		return &net.UDPAddr{IP: req.CIAddr, Port: s.ClientPort}
	}
	// Cliente sin IP todavía: difusión (no podemos resolver ARP para yiaddr).
	return &net.UDPAddr{IP: net.IPv4bcast, Port: s.ClientPort}
}

func (s *Server) count(t MessageType) {
	switch t {
	case Discover:
		s.Stats.Discover.Add(1)
	case Offer:
		s.Stats.Offer.Add(1)
	case Request:
		s.Stats.Request.Add(1)
	case Ack:
		s.Stats.Ack.Add(1)
	case Nak:
		s.Stats.Nak.Add(1)
	case Decline:
		s.Stats.Decline.Add(1)
	case Release:
		s.Stats.Release.Add(1)
	case Inform:
		s.Stats.Inform.Add(1)
	}
}

// Handle procesa un mensaje y devuelve la respuesta (nil si no hay).
func (s *Server) Handle(req *Packet, now time.Time) *Packet {
	d := s.d.Load()
	if d == nil || d.serverIP == nil {
		return nil
	}
	mt := req.MsgType()
	s.count(mt)
	mac := req.CHAddr.String()
	cl := d.cfg.FindClient(mac)
	name := req.Hostname()
	who := mac
	if name != "" {
		who = fmt.Sprintf("%s (%s)", mac, name)
	}

	// ¿Va dirigido a otro servidor?
	if sid := req.ServerID(); sid != nil && !sid.Equal(d.serverIP) {
		if mt == Request {
			s.DB.DropOffer(mac)
		}
		return nil
	}
	if cl != nil && cl.Deny {
		s.Logf("%s %s: cliente bloqueado, ignorado", mt, who)
		return nil
	}

	switch mt {
	case Discover:
		ip := s.allocate(d, mac, req.RequestedIP(), cl, now)
		if ip == nil {
			s.Logf("DISCOVER %s: sin direcciones libres", who)
			return nil
		}
		s.DB.SetOffer(mac, ip.String(), now.Add(offerHold))
		s.Logf("DISCOVER %s -> OFFER %s", who, ip)
		s.count(Offer)
		return s.reply(d, req, Offer, ip, s.leaseFor(d, cl, req), cl)

	case Request:
		ip := req.RequestedIP()
		if ip == nil {
			ip = req.CIAddr
		}
		if isZeroIP(ip) {
			return nil
		}
		ip = ip.To4()
		if !s.validRequest(d, mac, ip, cl, now) {
			known := req.ServerID() != nil || !isZeroIP(req.CIAddr) || d.network.Contains(ip)
			if d.cfg.Authoritative && known || req.ServerID() != nil {
				s.Logf("REQUEST %s %s -> NAK", who, ip)
				s.count(Nak)
				return s.nak(d, req, "dirección no disponible")
			}
			return nil
		}
		lt := s.leaseFor(d, cl, req)
		s.DB.Commit(Lease{MAC: mac, IP: ip.String(), Hostname: s.hostFor(cl, name), Vendor: req.VendorClass(), Start: now, Expiry: now.Add(lt)})
		s.Logf("REQUEST %s %s -> ACK (%s)", who, ip, config.Duration(lt))
		s.count(Ack)
		return s.reply(d, req, Ack, ip, lt, cl)

	case Decline:
		if ip := req.RequestedIP(); ip != nil {
			s.DB.MarkDeclined(ip.String(), now.Add(d.cfg.DeclineHold.D()))
			if l, ok := s.DB.Get(mac); ok && l.IP == ip.String() {
				s.DB.Delete(mac)
			}
			s.Logf("DECLINE %s rechaza %s (en conflicto): bloqueada %s", who, ip, d.cfg.DeclineHold)
		}
		return nil

	case Release:
		if l, ok := s.DB.Get(mac); ok && l.IP == req.CIAddr.To4().String() {
			s.DB.Delete(mac)
			s.Logf("RELEASE %s libera %s", who, l.IP)
		}
		return nil

	case Inform:
		if isZeroIP(req.CIAddr) {
			return nil
		}
		s.Logf("INFORM %s %s -> ACK", who, req.CIAddr)
		s.count(Ack)
		p := s.reply(d, req, Ack, nil, 0, cl)
		p.YIAddr = net.IPv4zero.To4()
		return p
	}
	return nil
}

func (s *Server) hostFor(cl *config.Client, reported string) string {
	if cl != nil && cl.Name != "" {
		return cl.Name
	}
	return reported
}

func (s *Server) leaseFor(d *derived, cl *config.Client, req *Packet) time.Duration {
	l := d.cfg.LeaseTime.D()
	if cl != nil && cl.LeaseTime > 0 {
		l = cl.LeaseTime.D()
	}
	// Si el cliente pide menos, se respeta.
	if r := time.Duration(req.RequestedLease()) * time.Second; r >= time.Minute && r < l {
		l = r
	}
	return l
}

func (d *derived) excluded(ip uint32) bool {
	for _, r := range d.excl {
		if ip >= r[0] && ip <= r[1] {
			return true
		}
	}
	return false
}

// usable: ¿puede ofrecerse ip (dinámica) a mac?
func (s *Server) usable(d *derived, ip uint32, mac string, now time.Time) bool {
	if ip < d.lo || ip > d.hi || d.excluded(ip) {
		return false
	}
	u := config.UintToIP(ip)
	if u.Equal(d.serverIP) {
		return false
	}
	if owner, ok := d.reserved[ip]; ok && owner != mac {
		return false
	}
	return !s.DB.Busy(u.String(), mac, now)
}

func (s *Server) allocate(d *derived, mac string, want net.IP, cl *config.Client, now time.Time) net.IP {
	if cl != nil && cl.IP != "" {
		ip := config.ParseIPv4(cl.IP)
		if s.DB.Busy(ip.String(), mac, now) {
			s.Logf("La IP reservada %s para %s está en uso por otro cliente", ip, mac)
			return nil
		}
		return ip
	}
	if l, ok := s.DB.Get(mac); ok {
		if ip := config.ParseIPv4(l.IP); ip != nil && s.usable(d, config.IPToUint(ip), mac, now) {
			return ip
		}
	}
	if want != nil && s.usable(d, config.IPToUint(want), mac, now) {
		return want.To4()
	}
	for ip := d.lo; ip <= d.hi && ip >= d.lo; ip++ {
		if s.usable(d, ip, mac, now) {
			return config.UintToIP(ip)
		}
		if ip == 0xFFFFFFFF {
			break
		}
	}
	return nil
}

func (s *Server) validRequest(d *derived, mac string, ip net.IP, cl *config.Client, now time.Time) bool {
	if !d.network.Contains(ip) || ip.Equal(d.serverIP) {
		return false
	}
	n := config.IPToUint(ip)
	if cl != nil && cl.IP != "" {
		if !config.ParseIPv4(cl.IP).Equal(ip) {
			return false
		}
	} else if owner, ok := d.reserved[n]; ok && owner != mac {
		return false
	}
	if s.DB.Busy(ip.String(), mac, now) {
		return false
	}
	if cl != nil && cl.IP != "" {
		return true
	}
	if l, ok := s.DB.Get(mac); ok && l.IP == ip.String() {
		return true // renovación de una concesión previa
	}
	return n >= d.lo && n <= d.hi && !d.excluded(n)
}

func (s *Server) base(d *derived, req *Packet, t MessageType) *Packet {
	p := &Packet{
		Op: BootReply, HType: req.HType, HLen: req.HLen, XID: req.XID, Flags: req.Flags,
		CIAddr: net.IPv4zero.To4(), YIAddr: net.IPv4zero.To4(), SIAddr: net.IPv4zero.To4(),
		GIAddr: req.GIAddr, CHAddr: req.CHAddr, Options: map[byte][]byte{},
	}
	p.Options[OptMsgType] = []byte{byte(t)}
	p.Options[OptServerID] = d.serverIP.To4()
	if id, ok := req.Options[OptClientID]; ok {
		p.Options[OptClientID] = id
	}
	return p
}

func (s *Server) nak(d *derived, req *Packet, msg string) *Packet {
	p := s.base(d, req, Nak)
	p.Options[OptMessage] = []byte(msg)
	return p
}

// reply construye OFFER/ACK. ip==nil en INFORM.
func (s *Server) reply(d *derived, req *Packet, t MessageType, ip net.IP, lease time.Duration, cl *config.Client) *Packet {
	p := s.base(d, req, t)
	if ip != nil {
		p.YIAddr = ip.To4()
		secs := uint32(lease / time.Second)
		p.SetU32(OptLeaseTime, secs)
		t1, t2 := secs/2, secs/8*7
		if d.cfg.RenewalTime > 0 {
			t1 = uint32(d.cfg.RenewalTime.D() / time.Second)
		}
		if d.cfg.RebindingTime > 0 {
			t2 = uint32(d.cfg.RebindingTime.D() / time.Second)
		}
		if t1 >= secs || t1 == 0 {
			t1 = secs / 2
		}
		if t2 >= secs || t2 <= t1 {
			t2 = secs / 8 * 7
		}
		p.SetU32(OptRenewalTime, t1)
		p.SetU32(OptRebindingTime, t2)
	}
	opts := d.cfg.Options
	if cl != nil {
		opts = opts.Merge(cl.Options)
		if cl.Name != "" {
			p.Options[OptHostname] = []byte(cl.Name)
		}
	}
	applyOptions(p, d, opts, t == Ack && ip == nil)
	return p
}

func ipList(l []string) []byte {
	var b []byte
	for _, s := range l {
		if ip := config.ParseIPv4(s); ip != nil {
			b = append(b, ip...)
		}
	}
	return b
}

func encodeDomains(l []string) []byte {
	var b []byte
	for _, d := range l {
		for _, lab := range strings.Split(strings.Trim(d, "."), ".") {
			if lab == "" || len(lab) > 63 {
				continue
			}
			b = append(b, byte(len(lab)))
			b = append(b, lab...)
		}
		b = append(b, 0)
	}
	return b
}

// encodeClassless codifica la opción 121 (RFC 3442).
func encodeClassless(routes []config.Route) []byte {
	var b []byte
	for _, r := range routes {
		_, n, err := net.ParseCIDR(r.Dest)
		gw := config.ParseIPv4(r.Gateway)
		if err != nil || gw == nil {
			continue
		}
		ones, _ := n.Mask.Size()
		b = append(b, byte(ones))
		b = append(b, n.IP.To4()[:(ones+7)/8]...)
		b = append(b, gw...)
	}
	return b
}

func applyOptions(p *Packet, d *derived, o config.Options, inform bool) {
	p.Options[OptSubnetMask] = []byte(net.IP(d.network.Mask).To4())
	bc := config.ParseIPv4(o.Broadcast)
	if bc == nil {
		n := config.IPToUint(d.network.IP) | ^binary.BigEndian.Uint32(d.network.Mask)
		bc = config.UintToIP(n)
	}
	p.Options[OptBroadcast] = bc
	if len(o.Routers) > 0 {
		p.Options[OptRouter] = ipList(o.Routers)
	}
	if len(o.DNS) > 0 {
		p.Options[OptDNS] = ipList(o.DNS)
	}
	if o.DomainName != "" {
		p.Options[OptDomainName] = []byte(o.DomainName)
	}
	if len(o.DomainSearch) > 0 {
		p.Options[OptDomainSearch] = encodeDomains(o.DomainSearch)
	}
	if len(o.NTP) > 0 {
		p.Options[OptNTP] = ipList(o.NTP)
	}
	if len(o.WINS) > 0 {
		p.Options[OptNetBIOSNS] = ipList(o.WINS)
	}
	if o.TimeOffset != nil {
		p.SetU32(OptTimeOffset, uint32(int32(*o.TimeOffset)))
	}
	if o.MTU > 0 {
		p.Options[OptMTU] = binary.BigEndian.AppendUint16(nil, uint16(o.MTU))
	}
	if o.TFTPServer != "" {
		p.Options[OptTFTPServer] = []byte(o.TFTPServer)
		p.SName = o.TFTPServer
	}
	if o.BootFile != "" {
		p.Options[OptBootFile] = []byte(o.BootFile)
		p.File = o.BootFile
	}
	if ns := config.ParseIPv4(o.NextServer); ns != nil {
		p.SIAddr = ns
	}
	if len(o.Routes) > 0 {
		routes := append([]config.Route(nil), o.Routes...)
		// RFC 3442: con la opción 121 el cliente ignora la 3; añadir ruta por defecto.
		hasDefault := false
		for _, r := range routes {
			if strings.HasPrefix(r.Dest, "0.0.0.0/0") {
				hasDefault = true
			}
		}
		if !hasDefault && len(o.Routers) > 0 {
			routes = append(routes, config.Route{Dest: "0.0.0.0/0", Gateway: o.Routers[0]})
		}
		p.Options[OptClasslessRoute] = encodeClassless(routes)
	}
	for _, c := range o.Custom {
		if b, err := c.Encode(); err == nil {
			p.Options[byte(c.Code)] = b
		}
	}
}
