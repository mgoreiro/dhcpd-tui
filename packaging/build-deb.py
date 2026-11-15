#!/usr/bin/env python3
"""Genera paquetes .deb sin necesitar dpkg (ar + tar.gz a mano).

uso: build-deb.py VERSION DEBARCH BINARIO UNIDAD.service SALIDA.deb
"""
import io, os, sys, tarfile, time, gzip

version, arch, binary, unit_src, out = sys.argv[1:6]
now = int(time.time())

def tar_gz(entries):
    raw = io.BytesIO()
    with tarfile.open(fileobj=raw, mode="w", format=tarfile.GNU_FORMAT) as t:
        for name, kind, mode, data in entries:
            ti = tarfile.TarInfo(name)
            ti.mtime, ti.uid, ti.gid, ti.uname, ti.gname, ti.mode = now, 0, 0, "root", "root", mode
            if kind == "dir":
                ti.type = tarfile.DIRTYPE
                t.addfile(ti)
            else:
                ti.size = len(data)
                t.addfile(ti, io.BytesIO(data))
    gz = io.BytesIO()
    with gzip.GzipFile(fileobj=gz, mode="wb", mtime=now, compresslevel=9) as g:
        g.write(raw.getvalue())
    return gz.getvalue()

bin_data = open(binary, "rb").read()
unit = open(unit_src).read().replace("@BINDIR@", "/usr/bin").encode()
readme = (b"dhcpd-tui: servidor DHCP con interfaz Turbo Vision.\n"
          b"Administrar:  sudo dhcpd-tui -no-server\n")

data = tar_gz([
    ("./", "dir", 0o755, b""),
    ("./usr/", "dir", 0o755, b""),
    ("./usr/bin/", "dir", 0o755, b""),
    ("./usr/bin/dhcpd-tui", "file", 0o755, bin_data),
    ("./lib/", "dir", 0o755, b""),
    ("./lib/systemd/", "dir", 0o755, b""),
    ("./lib/systemd/system/", "dir", 0o755, b""),
    ("./lib/systemd/system/dhcpd-tui.service", "file", 0o644, unit),
    ("./etc/", "dir", 0o755, b""),
    ("./etc/dhcpd-tui/", "dir", 0o755, b""),
    ("./usr/share/", "dir", 0o755, b""),
    ("./usr/share/doc/", "dir", 0o755, b""),
    ("./usr/share/doc/dhcpd-tui/", "dir", 0o755, b""),
    ("./usr/share/doc/dhcpd-tui/README", "file", 0o644, readme),
])

size_kb = (len(bin_data) + len(unit) + len(readme) + 1023) // 1024
control = f"""Package: dhcpd-tui
Version: {version}
Section: net
Priority: optional
Architecture: {arch}
Installed-Size: {size_kb}
Maintainer: dhcpd-tui <root@localhost>
Description: Servidor DHCP autonomo con interfaz de texto estilo Turbo Vision
 Servidor DHCPv4 en un unico binario estatico. Permite reservar IP y aplicar
 configuracion individual (DNS, router, lease...) a cada cliente desde una TUI.
 El servicio systemd se activa en la instalacion; administre con
 "dhcpd-tui -no-server".
""".encode()

def script(body):
    return ("#!/bin/sh\nset -e\n" + body).encode()

postinst = script("""case "$1" in
  configure)
    mkdir -p /etc/dhcpd-tui
    if [ -d /run/systemd/system ]; then
      systemctl daemon-reload || true
      systemctl enable dhcpd-tui || true
      systemctl restart dhcpd-tui || echo "dhcpd-tui: no se pudo iniciar; revise 'journalctl -u dhcpd-tui'" >&2
    fi
    ;;
esac
exit 0
""")
prerm = script("""case "$1" in
  remove|deconfigure)
    if [ -d /run/systemd/system ]; then
      systemctl disable --now dhcpd-tui || true
    fi
    ;;
esac
exit 0
""")
postrm = script("""case "$1" in
  remove|purge)
    [ -d /run/systemd/system ] && systemctl daemon-reload || true
    ;;
  purge)
    ;;
esac
if [ "$1" = purge ]; then rm -rf /etc/dhcpd-tui; fi
exit 0
""")

md5 = "".join(
    f"{__import__('hashlib').md5(d).hexdigest()}  {p}\n"
    for p, d in [("usr/bin/dhcpd-tui", bin_data),
                 ("lib/systemd/system/dhcpd-tui.service", unit),
                 ("usr/share/doc/dhcpd-tui/README", readme)]).encode()

ctrl = tar_gz([
    ("./", "dir", 0o755, b""),
    ("./control", "file", 0o644, control),
    ("./md5sums", "file", 0o644, md5),
    ("./postinst", "file", 0o755, postinst),
    ("./prerm", "file", 0o755, prerm),
    ("./postrm", "file", 0o755, postrm),
])

def ar_member(name, payload):
    hdr = f"{name:<16}{now:<12}{0:<6}{0:<6}{'100644':<8}{len(payload):<10}`\n".encode()
    return hdr + payload + (b"\n" if len(payload) % 2 else b"")

os.makedirs(os.path.dirname(out) or ".", exist_ok=True)
with open(out, "wb") as f:
    f.write(b"!<arch>\n")
    f.write(ar_member("debian-binary", b"2.0\n"))
    f.write(ar_member("control.tar.gz", ctrl))
    f.write(ar_member("data.tar.gz", data))
print("creado", out)
