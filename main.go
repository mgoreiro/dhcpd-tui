// SPDX-License-Identifier: GPL-3.0-or-later

// dhcpd-tui: servidor DHCP autónomo con interfaz de texto estilo Turbo Vision.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"dhcpd-tui/internal/app"
	"dhcpd-tui/internal/tui"
)

var version = "dev"

func main() {
	dir := flag.String("dir", app.DefaultDir(), "directorio con config.json y leases.json")
	headless := flag.Bool("headless", false, "solo servidor DHCP, sin interfaz (para systemd)")
	noServer := flag.Bool("no-server", false, "solo interfaz: edita config.json y muestra leases de un servidor -headless")
	port := flag.Int("port", 67, "puerto UDP del servidor (solo pruebas)")
	cport := flag.Int("client-port", 68, "puerto UDP de los clientes (solo pruebas)")
	service := flag.String("service", "dhcpd-tui", "unidad systemd que controla la TUI con -no-server")
	showVer := flag.Bool("version", false, "mostrar versión")
	flag.Parse()

	if *showVer {
		fmt.Println("dhcpd-tui", version)
		return
	}
	if *headless && *noServer {
		fmt.Fprintln(os.Stderr, "-headless y -no-server son incompatibles")
		os.Exit(2)
	}

	core, err := app.NewCore(*dir, !*noServer, *port, *cport)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	core.ServiceName = *service
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go core.Watch(ctx)
	if core.ServerMode {
		core.DB.StartFlusher()
	}
	defer core.DB.Close()

	if *headless {
		core.Log.Echo = os.Stdout
		if err := core.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		core.Stop()
		return
	}

	err = tui.Run(core)
	core.Stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
