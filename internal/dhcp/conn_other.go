//go:build !linux

package dhcp

import (
	"context"
	"fmt"
	"net"
	"syscall"
)

// Versión para desarrollo en otros sistemas (no ligada a interfaz).
func listenUDP(iface string, port int) (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: func(network, address string, c syscall.RawConn) error {
		return c.Control(func(fd uintptr) {
			_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
			_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
		})
	}}
	pc, err := lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}
