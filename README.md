# dhcpd-tui

Servidor DHCP autónomo para Linux (ARM, ARM64 y x86) con interfaz de texto
estilo **Turbo Vision**. Un único binario estático, sin dependencias.

## Compilar (desde cualquier máquina con Go ≥ 1.22)

```bash
make all        # dist/dhcpd-tui-linux-{arm64,armv7,armv6,amd64}
make test
```

| Binario | Dispositivos típicos |
|---|---|
| `arm64` | Raspberry Pi 3/4/5 (SO 64 bits), Orange Pi, servidores ARM |
| `armv7` | Raspberry Pi 2/3 (SO 32 bits), BeagleBone |
| `armv6` | Raspberry Pi 1 / Zero |

## Uso

```bash
sudo ./dhcpd-tui                 # servidor + interfaz (datos en /etc/dhcpd-tui)
sudo ./dhcpd-tui -headless       # solo servidor (systemd), registro por stdout
./dhcpd-tui -no-server           # solo interfaz: edita config.json y ve los leases
                                 # de un servidor -headless que corra aparte
```

Necesita root (o `CAP_NET_BIND_SERVICE` + `CAP_NET_RAW`) para el puerto UDP 67.
La interfaz de red debe tener una IP estática dentro de la subred que se sirve.
**No ejecute dos servidores DHCP en la misma red.**

Como servicio: copie el binario a `/usr/local/bin`, `contrib/dhcpd-tui.service`
a `/etc/systemd/system/` y ejecute `systemctl enable --now dhcpd-tui`.
Después puede abrir `dhcpd-tui -no-server -dir /etc/dhcpd-tui` para administrarlo;
el servicio recoge los cambios del `config.json` en ≤ 2 s sin reiniciar.

## Instalación

**Instalador** (copia el binario de tu arquitectura, instala la unidad y activa el servicio):

```bash
sudo ./install.sh              # --no-start: solo habilita; --uninstall: desinstala
```

**Paquetes Debian/Raspberry Pi OS** (`make deb` → `dist/*.deb`):

| Paquete | Sistema |
|---|---|
| `dhcpd-tui_*_arm64.deb` | Debian/Ubuntu/Pi OS 64 bits |
| `dhcpd-tui_*_armhf.deb` | Pi OS 32 bits (vale para ARMv6 y ARMv7) |
| `dhcpd-tui_*_armel.deb` | Debian armel |
| `dhcpd-tui_*_amd64.deb` | x86-64 |

```bash
sudo apt install ./dhcpd-tui_1.0.0_arm64.deb   # instala, habilita e inicia el servicio
sudo dhcpd-tui -no-server                      # administrar
```

Al instalar se crea una configuración inicial a partir de la primera interfaz
con IP; revísela (F2) antes de conectar el equipo a una red con otro DHCP.
`apt remove` detiene y deshabilita el servicio; `apt purge` borra `/etc/dhcpd-tui`.

## Interfaz

Barra de menús (F10 o Alt+letra, también con ratón), ventana de estado,
lista de clientes, registro y barra de atajos.

| Tecla | Acción | Tecla | Acción |
|---|---|---|---|
| F2 / c | Configuración del servidor | F5 / s | Iniciar / parar |
| F3 / o | Opciones DHCP globales | F6 / r | Reservar la IP actual del cliente |
| F4, Intro / e | Editar el cliente seleccionado | F7 / b | Bloquear / permitir |
| Ins / n | Nuevo cliente por MAC | F8 / f | Olvidar lease |
| F9 | Servicio systemd al arrancar el sistema (con `-no-server`) | F5 | con `-no-server` inicia/para el servicio systemd |
| Del | Eliminar perfil | Alt-X / q | Salir |

### Configuración individual por cliente (Intro sobre un cliente)
MAC, nombre de host, **IP reservada**, lease propio, y opciones propias:
router, **DNS**, dominio, NTP y opciones personalizadas. Lo que se deja vacío
hereda la configuración global. Los perfiles se guardan en `config.json`
y persisten aunque el cliente no esté conectado.

### Lease
Por defecto **6 horas**. Se cambia en F2 (formatos `6h`, `90m`, `1d12h`, `3600`)
o por cliente. Si un cliente pide menos tiempo se le respeta. T1/T2 son 50 % y
87,5 % salvo que se fijen en F3.

## Opciones DHCP soportadas
Máscara (1), desfase horario (2), router (3), DNS (6), nombre de host (12),
dominio (15), MTU (26), broadcast (28), NTP (42), WINS (44), lease (51),
T1/T2 (58/59), TFTP (66), fichero de arranque (67), dominios de búsqueda (119),
rutas estáticas sin clase (121, con ruta por defecto añadida automáticamente),
`siaddr`/next-server, y **cualquier otra** con `código:tipo:valor`
(tipos `string, ip, ips, u8, u16, u32, hex`). Mensajes: DISCOVER/OFFER,
REQUEST/ACK/NAK (incluye INIT-REBOOT y renovación), DECLINE (la IP se bloquea
10 min), RELEASE, INFORM, relay (giaddr), exclusiones de rango, modo
autoritativo y bloqueo de clientes.

## Ficheros
`config.json` (configuración y perfiles; escritura atómica) y `leases.json`
(se guarda cada 2 s si hay cambios, para cuidar las tarjetas SD; el historial
de leases caducados se conserva 30 días).

## Límites actuales
Una subred/interfaz por instancia; solo IPv4. Las respuestas a clientes que
aún no tienen IP se envían por difusión.

## Licencia
GNU General Public License v3.0 o posterior (`GPL-3.0-or-later`). Ver [LICENSE](LICENSE).
