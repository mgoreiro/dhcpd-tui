//go:build linux

package dhcp

import (
	"context"
	"fmt"
	"net"
	"syscall"
)

// listenUDP abre el socket del servidor ligado a una interfaz concreta
// (SO_BINDTODEVICE), con difusión permitida.
func listenUDP(iface string, port int) (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: func(network, address string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			if serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); serr != nil {
				return
			}
			if serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1); serr != nil {
				return
			}
			if iface != "" {
				serr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
			}
		})
		if err != nil {
			return err
		}
		return serr
	}}
	pc, err := lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}
