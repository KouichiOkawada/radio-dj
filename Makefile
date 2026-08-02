.PHONY: build install clean

BIN := radio-dj
LDFLAGS := -s -w

# Stripped build: drops DWARF symbol tables (disk 8 MB vs 12 MB; RAM unaffected).
build:
	go build -ldflags="$(LDFLAGS)" -o $(BIN) .

# Installs as launchd (macOS) / systemd (Linux) service. `radio-dj install`
# copies this binary to ~/.local/bin and re-signs it ad-hoc on macOS.
install: build
	./$(BIN) install

clean:
	rm -f $(BIN)
