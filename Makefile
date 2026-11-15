VERSION ?= 1.0.0
LDFLAGS := -s -w -X main.version=$(VERSION)
GO      := CGO_ENABLED=0 go

.PHONY: all test clean arm64 armv7 armv6 armv5 amd64 native deb

all: arm64 armv7 armv6 armv5 amd64

dist:
	mkdir -p dist

# Raspberry Pi 3/4/5, placas ARM de 64 bits
arm64: dist
	GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/dhcpd-tui-linux-arm64 .

# Raspberry Pi 2/3 con sistema de 32 bits, BeagleBone, etc.
armv7: dist
	GOOS=linux GOARCH=arm GOARM=7 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/dhcpd-tui-linux-armv7 .

# Raspberry Pi 1 / Zero
armv6: dist
	GOOS=linux GOARCH=arm GOARM=6 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/dhcpd-tui-linux-armv6 .

# Debian armel (sin coma flotante por hardware)
armv5: dist
	GOOS=linux GOARCH=arm GOARM=5 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/dhcpd-tui-linux-armv5 .

amd64: dist
	GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/dhcpd-tui-linux-amd64 .

# Paquetes Debian: arm64, armhf (valido para ARMv6 y ARMv7), armel y amd64
deb: all
	python3 packaging/build-deb.py $(VERSION) arm64 dist/dhcpd-tui-linux-arm64 contrib/dhcpd-tui.service dist/dhcpd-tui_$(VERSION)_arm64.deb
	python3 packaging/build-deb.py $(VERSION) armhf dist/dhcpd-tui-linux-armv6 contrib/dhcpd-tui.service dist/dhcpd-tui_$(VERSION)_armhf.deb
	python3 packaging/build-deb.py $(VERSION) armel dist/dhcpd-tui-linux-armv5 contrib/dhcpd-tui.service dist/dhcpd-tui_$(VERSION)_armel.deb
	python3 packaging/build-deb.py $(VERSION) amd64 dist/dhcpd-tui-linux-amd64 contrib/dhcpd-tui.service dist/dhcpd-tui_$(VERSION)_amd64.deb

native:
	$(GO) build -ldflags "$(LDFLAGS)" -o dhcpd-tui .

test:
	go vet ./... && go test ./...

clean:
	rm -rf dist dhcpd-tui
