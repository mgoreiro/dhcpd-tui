package dhcp

import (
	"net"
	"testing"
	"time"

	"dhcpd-tui/internal/config"
)

// Prueba extremo a extremo por UDP real (puertos altos). Se omite si el
// sistema no entrega la difusión a 255.255.255.255.
func TestUDPEndToEnd(t *testing.T) {
	cfg := config.Default()
	cfg.Interface, cfg.ServerIP, cfg.Subnet = "lo", "192.168.77.1", "192.168.77.0/24"
	cfg.RangeStart, cfg.RangeEnd = "192.168.77.10", "192.168.77.20"
	cfg.Options = config.Options{Routers: []string{"192.168.77.1"}}
	s, err := New(cfg, NewDB(""), t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	s.ServerPort, s.ClientPort = 16767, 16768
	if err := s.Start(); err != nil {
		t.Skipf("no se puede abrir el socket: %v", err)
	}
	defer s.Stop()

	cl, err := listenUDP("", 16768)
	if err != nil {
		t.Skip(err)
	}
	defer cl.Close()
	p := req(Discover, "02:00:00:00:00:99", nil)
	if _, err := cl.WriteToUDP(p.Marshal(), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 16767}); err != nil {
		t.Fatal(err)
	}
	cl.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, _, err := cl.ReadFromUDP(buf)
	if err != nil {
		t.Skipf("difusión no entregada en este sistema: %v", err)
	}
	// El propio paquete DISCOVER enviado a otro puerto no llega aquí; lo recibido es la OFFER.
	off, err := Parse(buf[:n])
	if err != nil || off.MsgType() != Offer || off.YIAddr.String() != "192.168.77.10" {
		t.Fatalf("respuesta inesperada: %v %+v", err, off)
	}
}
