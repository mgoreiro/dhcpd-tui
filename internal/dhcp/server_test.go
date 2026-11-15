package dhcp

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"dhcpd-tui/internal/config"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	cfg := config.Default()
	cfg.Interface = "test0"
	cfg.ServerIP = "192.168.1.1"
	cfg.Subnet = "192.168.1.0/24"
	cfg.RangeStart, cfg.RangeEnd = "192.168.1.100", "192.168.1.102"
	cfg.Options = config.Options{Routers: []string{"192.168.1.1"}, DNS: []string{"1.1.1.1"}, DomainName: "lan"}
	cfg.Clients = []config.Client{
		{MAC: "aa:bb:cc:00:00:01", Name: "nas", IP: "192.168.1.50",
			Options: config.Options{DNS: []string{"192.168.1.2"}}},
		{MAC: "aa:bb:cc:00:00:02", Deny: true},
	}
	s, err := New(cfg, NewDB(""), t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func req(t MessageType, mac string, opts map[byte][]byte) *Packet {
	hw, _ := net.ParseMAC(mac)
	p := &Packet{Op: BootRequest, HType: 1, HLen: 6, XID: 42, CIAddr: net.IPv4zero, YIAddr: net.IPv4zero,
		SIAddr: net.IPv4zero, GIAddr: net.IPv4zero, CHAddr: hw, Options: map[byte][]byte{OptMsgType: {byte(t)}}}
	for k, v := range opts {
		p.Options[k] = v
	}
	return p
}

func u32(p *Packet, c byte) uint32 { return binary.BigEndian.Uint32(p.Options[c]) }

func TestRoundTrip(t *testing.T) {
	p := req(Discover, "aa:bb:cc:dd:ee:ff", map[byte][]byte{OptHostname: []byte("pc1"), 250: make([]byte, 300)})
	q, err := Parse(p.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if q.CHAddr.String() != "aa:bb:cc:dd:ee:ff" || q.Hostname() != "pc1" || q.XID != 42 || q.MsgType() != Discover {
		t.Fatalf("round trip falló: %+v", q)
	}
	if len(q.Options[250]) != 300 {
		t.Fatalf("opción larga (RFC 3396): %d", len(q.Options[250]))
	}
}

func TestDORAAndDefaultLease(t *testing.T) {
	s := testServer(t)
	now := time.Now()
	mac := "de:ad:be:ef:00:01"
	off := s.Handle(req(Discover, mac, nil), now)
	if off == nil || off.MsgType() != Offer || off.YIAddr.String() != "192.168.1.100" {
		t.Fatalf("oferta inesperada: %+v", off)
	}
	if u32(off, OptLeaseTime) != 6*3600 {
		t.Fatalf("lease por defecto debe ser 6h, es %d", u32(off, OptLeaseTime))
	}
	if u32(off, OptRenewalTime) != 3*3600 || u32(off, OptRebindingTime) != 6*3600/8*7 {
		t.Fatal("T1/T2 incorrectos")
	}
	if off.Options[OptSubnetMask][3] != 0 || net.IP(off.Options[OptRouter]).String() != "192.168.1.1" {
		t.Fatal("máscara/router")
	}
	ack := s.Handle(req(Request, mac, map[byte][]byte{OptRequestedIP: off.YIAddr.To4(), OptServerID: net.IP{192, 168, 1, 1}}), now)
	if ack == nil || ack.MsgType() != Ack {
		t.Fatalf("esperaba ACK: %+v", ack)
	}
	if l, ok := s.DB.Get(mac); !ok || l.IP != "192.168.1.100" || !l.Active(now) {
		t.Fatalf("lease no guardado: %+v", l)
	}
	// Mismo cliente vuelve a descubrir: misma IP.
	if o2 := s.Handle(req(Discover, mac, nil), now); o2.YIAddr.String() != "192.168.1.100" {
		t.Fatal("el cliente debe conservar su IP")
	}
	// Otro cliente: siguiente IP.
	if o3 := s.Handle(req(Discover, "de:ad:be:ef:00:02", nil), now); o3.YIAddr.String() != "192.168.1.101" {
		t.Fatalf("segundo cliente: %s", o3.YIAddr)
	}
}

func TestClientShorterLeaseHonored(t *testing.T) {
	s := testServer(t)
	p := req(Discover, "de:ad:be:ef:00:09", map[byte][]byte{OptLeaseTime: binary.BigEndian.AppendUint32(nil, 600)})
	if o := s.Handle(p, time.Now()); u32(o, OptLeaseTime) != 600 {
		t.Fatalf("lease pedido de 600s: %d", u32(o, OptLeaseTime))
	}
}

func TestReservationAndOverrides(t *testing.T) {
	s := testServer(t)
	off := s.Handle(req(Discover, "aa:bb:cc:00:00:01", map[byte][]byte{OptHostname: []byte("otro")}), time.Now())
	if off.YIAddr.String() != "192.168.1.50" {
		t.Fatalf("reserva: %s", off.YIAddr)
	}
	if net.IP(off.Options[OptDNS]).String() != "192.168.1.2" {
		t.Fatal("DNS específico del cliente no aplicado")
	}
	if string(off.Options[OptDomainName]) != "lan" {
		t.Fatal("el dominio global debe heredarse")
	}
	if string(off.Options[OptHostname]) != "nas" {
		t.Fatal("nombre de host asignado")
	}
	// Otro cliente nunca recibe una IP reservada, aunque la pida.
	o := s.Handle(req(Discover, "de:ad:be:ef:00:03", map[byte][]byte{OptRequestedIP: net.IP{192, 168, 1, 50}}), time.Now())
	if o.YIAddr.String() == "192.168.1.50" {
		t.Fatal("IP reservada entregada a otro")
	}
	// El titular pidiendo otra IP recibe NAK.
	nak := s.Handle(req(Request, "aa:bb:cc:00:00:01", map[byte][]byte{OptRequestedIP: net.IP{192, 168, 1, 101}}), time.Now())
	if nak == nil || nak.MsgType() != Nak {
		t.Fatal("esperaba NAK")
	}
}

func TestDenyAndOtherServer(t *testing.T) {
	s := testServer(t)
	if s.Handle(req(Discover, "aa:bb:cc:00:00:02", nil), time.Now()) != nil {
		t.Fatal("cliente bloqueado debe ignorarse")
	}
	if s.Handle(req(Request, "de:ad:be:ef:00:05", map[byte][]byte{OptServerID: net.IP{10, 0, 0, 1}, OptRequestedIP: net.IP{192, 168, 1, 100}}), time.Now()) != nil {
		t.Fatal("petición para otro servidor debe ignorarse")
	}
}

func TestPoolExhaustionReleaseDecline(t *testing.T) {
	s := testServer(t)
	now := time.Now()
	macs := []string{"de:ad:be:ef:01:01", "de:ad:be:ef:01:02", "de:ad:be:ef:01:03"}
	for _, m := range macs {
		o := s.Handle(req(Discover, m, nil), now)
		s.Handle(req(Request, m, map[byte][]byte{OptRequestedIP: o.YIAddr.To4(), OptServerID: net.IP{192, 168, 1, 1}}), now)
	}
	if s.Handle(req(Discover, "de:ad:be:ef:01:04", nil), now) != nil {
		t.Fatal("pool agotado: no debe haber oferta")
	}
	// Release libera.
	rel := req(Release, macs[0], nil)
	rel.CIAddr = net.IP{192, 168, 1, 100}
	s.Handle(rel, now)
	o := s.Handle(req(Discover, "de:ad:be:ef:01:04", nil), now)
	if o == nil || o.YIAddr.String() != "192.168.1.100" {
		t.Fatalf("tras RELEASE debe reutilizarse 192.168.1.100: %+v", o)
	}
	// Decline bloquea la IP.
	s.Handle(req(Decline, macs[1], map[byte][]byte{OptRequestedIP: net.IP{192, 168, 1, 101}}), now)
	s.DB.Delete(macs[2])
	s.DB.DropOffer("de:ad:be:ef:01:04")
	o = s.Handle(req(Discover, "de:ad:be:ef:01:09", nil), now)
	if o.YIAddr.String() == "192.168.1.101" {
		t.Fatal("IP rechazada (DECLINE) no debe ofrecerse")
	}
	// Expiración: pasadas 6h el lease deja de estar activo y la IP vuelve a estar libre.
	if s.DB.Busy("192.168.1.100", "x", now.Add(7*time.Hour)) {
		t.Fatal("lease expirado debe liberar la IP")
	}
}

func TestInformAndOptions(t *testing.T) {
	s := testServer(t)
	cfg := s.Config()
	to := -3600
	cfg.Options.TimeOffset = &to
	cfg.Options.Routes = []config.Route{{Dest: "10.0.0.0/8", Gateway: "192.168.1.254"}}
	cfg.Options.DomainSearch = []string{"lan.example.com"}
	cfg.Options.Custom = []config.CustomOption{{Code: 150, Type: "ip", Value: "192.168.1.9"}}
	cfg.Options.TFTPServer, cfg.Options.BootFile = "192.168.1.9", "pxelinux.0"
	if err := s.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	r := req(Inform, "de:ad:be:ef:02:01", nil)
	r.CIAddr = net.IP{192, 168, 1, 77}
	a := s.Handle(r, time.Now())
	if a == nil || a.MsgType() != Ack || !isZeroIP(a.YIAddr) {
		t.Fatalf("INFORM: %+v", a)
	}
	if _, ok := a.Options[OptLeaseTime]; ok {
		t.Fatal("INFORM no debe llevar lease")
	}
	// 10.0.0.0/8 via .254 + ruta por defecto via router
	want := []byte{8, 10, 192, 168, 1, 254, 0, 192, 168, 1, 1}
	if string(a.Options[OptClasslessRoute]) != string(want) {
		t.Fatalf("opción 121: %v", a.Options[OptClasslessRoute])
	}
	if net.IP(a.Options[150]).String() != "192.168.1.9" || a.File != "pxelinux.0" || int32(u32(a, OptTimeOffset)) != -3600 {
		t.Fatal("opciones TFTP/personalizada/offset")
	}
	if len(a.Options[OptDomainSearch]) == 0 {
		t.Fatal("search domains")
	}
	if _, err := Parse(a.Marshal()); err != nil {
		t.Fatal(err)
	}
}

func TestConfigValidation(t *testing.T) {
	c := config.Default()
	c.Subnet, c.RangeStart, c.RangeEnd, c.ServerIP = "10.0.0.0/24", "10.0.0.10", "10.0.0.5", ""
	if c.Validate() == nil {
		t.Fatal("rango invertido debe fallar")
	}
	c.RangeEnd = "10.0.1.5"
	if c.Validate() == nil {
		t.Fatal("rango fuera de subred debe fallar")
	}
	c.RangeEnd = "10.0.0.50"
	c.Clients = []config.Client{{MAC: "aa:aa:aa:aa:aa:01", IP: "10.0.0.20"}, {MAC: "aa:aa:aa:aa:aa:02", IP: "10.0.0.20"}}
	if c.Validate() == nil {
		t.Fatal("IP reservada duplicada debe fallar")
	}
	for in, want := range map[string]time.Duration{"6h": 6 * time.Hour, "1d12h": 36 * time.Hour, "90m": 90 * time.Minute, "3600": time.Hour} {
		if d, err := config.ParseDuration(in); err != nil || d.D() != want {
			t.Fatalf("ParseDuration(%q)=%v,%v", in, d, err)
		}
	}
	if config.Duration(36*time.Hour).String() != "1d12h" {
		t.Fatal("String duración")
	}
}
