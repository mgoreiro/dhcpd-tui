#!/bin/sh
# Instalador de dhcpd-tui: copia el binario, instala la unidad systemd y
# activa el servicio.  Uso: sudo ./install.sh [--no-start] [--bindir DIR] | --uninstall
set -eu

BINDIR=/usr/local/bin
CONFDIR=/etc/dhcpd-tui
UNIT=/etc/systemd/system/dhcpd-tui.service
START=1
ACTION=install
HERE=$(cd "$(dirname "$0")" && pwd)

while [ $# -gt 0 ]; do
  case "$1" in
    --no-start) START=0 ;;
    --bindir) BINDIR=$2; shift ;;
    --uninstall) ACTION=uninstall ;;
    -h|--help) sed -n '2,3p' "$0"; exit 0 ;;
    *) echo "opción desconocida: $1" >&2; exit 2 ;;
  esac
  shift
done

[ "$(id -u)" -eq 0 ] || { echo "Ejecute como root (sudo $0)" >&2; exit 1; }
command -v systemctl >/dev/null 2>&1 || { echo "Se necesita systemd (systemctl)" >&2; exit 1; }

if [ "$ACTION" = uninstall ]; then
  systemctl disable --now dhcpd-tui 2>/dev/null || true
  rm -f "$UNIT" "$BINDIR/dhcpd-tui"
  systemctl daemon-reload
  echo "Desinstalado. La configuración en $CONFDIR se ha conservado."
  exit 0
fi

case "$(uname -m)" in
  aarch64|arm64)  ARCH=arm64 ;;
  armv7l|armv8l)  ARCH=armv7 ;;
  armv6l)         ARCH=armv6 ;;
  x86_64|amd64)   ARCH=amd64 ;;
  *) echo "Arquitectura no soportada: $(uname -m)" >&2; exit 1 ;;
esac

BIN=
for c in "$HERE/dhcpd-tui" "$HERE/dist/dhcpd-tui-linux-$ARCH" "$HERE/dhcpd-tui-linux-$ARCH"; do
  [ -f "$c" ] && { BIN=$c; break; }
done
[ -n "$BIN" ] || { echo "No encuentro el binario para $ARCH (¿ejecutó 'make all'?)" >&2; exit 1; }

UNITSRC="$HERE/contrib/dhcpd-tui.service"
[ -f "$UNITSRC" ] || UNITSRC="$HERE/dhcpd-tui.service"
[ -f "$UNITSRC" ] || { echo "Falta contrib/dhcpd-tui.service" >&2; exit 1; }

for other in dnsmasq isc-dhcp-server kea-dhcp4-server udhcpd; do
  if systemctl is-active --quiet "$other" 2>/dev/null; then
    echo "AVISO: $other está activo y puede competir por el puerto DHCP (67)." >&2
  fi
done

install -d -m 755 "$BINDIR" "$CONFDIR"
install -m 755 "$BIN" "$BINDIR/dhcpd-tui"
sed "s#@BINDIR@#$BINDIR#g" "$UNITSRC" > "$UNIT"
chmod 644 "$UNIT"
systemctl daemon-reload

if [ "$START" -eq 1 ]; then
  systemctl enable --now dhcpd-tui
  echo "Servicio activado y en marcha."
else
  systemctl enable dhcpd-tui
  echo "Servicio activado para el arranque (no iniciado)."
fi
echo "Administración:  sudo dhcpd-tui -no-server -dir $CONFDIR"
echo "Estado:          systemctl status dhcpd-tui"
