// Package supervisor spawns and watches the icecast broadcast server as a
// child process, so radio-dj is self-contained: one binary brings up
// everything it needs. If icecast dies, it is restarted automatically.
package supervisor

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// Icecast manages a child icecast process.
type Icecast struct {
	bin        string
	configPath string
	host       string
	port       int
	sourcePw   string
	adminPw    string
	cmd        *exec.Cmd
}

// EnsureIcecast locates the icecast binary, reuses the existing config under
// stateDir (stable password across restarts) or writes a fresh one, kills any
// stray icecast, starts its own, waits for the port, and supervises it.
// findIcecast locates the icecast binary: first on PATH, then in the usual
// Homebrew locations (launchd and other minimal-PATH contexts miss /opt/homebrew/bin).
func findIcecast() (string, error) {
	if bin, err := exec.LookPath("icecast"); err == nil {
		return bin, nil
	}
	for _, p := range []string{
		"/opt/homebrew/bin/icecast", // macOS Apple Silicon
		"/usr/local/bin/icecast",   // macOS Intel / manual install
		"/opt/homebrew/opt/icecast/bin/icecast",
		"/usr/bin/icecast", // Linux
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("icecast binary not found — install it (macOS: `brew install icecast`)")
}

func EnsureIcecast(stateDir, host string, port int) (*Icecast, error) {
	bin, err := findIcecast()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "logs"), 0o755); err != nil {
		return nil, err
	}
	cfgPath := filepath.Join(stateDir, "icecast.xml")
	var src, adm string
	if existing, err := os.ReadFile(cfgPath); err == nil {
		// reuse the persisted config so the passwords stay stable across restarts
		if pw := readSourcePw(string(existing)); pw != "" {
			src = pw
		}
		if pw := readAdminPw(string(existing)); pw != "" {
			adm = pw
		}
	}
	if src == "" {
		src = randHex(12)
		adm = randHex(12)
		if err := os.WriteFile(cfgPath, []byte(renderConfig(stateDir, host, port, src, adm)), 0o644); err != nil {
			return nil, err
		}
	}
	// kill any stray icecast so OUR config (and password) is the one on :port
	killStrayIcecast()
	ic := &Icecast{bin: bin, configPath: cfgPath, host: host, port: port, sourcePw: src, adminPw: adm}
	if err := ic.start(); err != nil {
		return nil, err
	}
	if err := ic.waitPort(15 * time.Second); err != nil {
		return nil, err
	}
	go ic.supervise()
	return ic, nil
}

// SourcePassword is the icecast source password radio-dj streams with.
func (ic *Icecast) SourcePassword() string { return ic.sourcePw }

// AdminPassword is the icecast admin password (for /admin/stats listener counts).
func (ic *Icecast) AdminPassword() string { return ic.adminPw }

func (ic *Icecast) start() error {
	cmd := exec.Command(ic.bin, "-c", ic.configPath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start icecast: %w", err)
	}
	ic.cmd = cmd
	return nil
}

func (ic *Icecast) waitPort(max time.Duration) error {
	addr := net.JoinHostPort(ic.host, strconv.Itoa(ic.port))
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("icecast did not open %s within %s", addr, max)
}

// supervise restarts icecast whenever it exits. Runs forever.
func (ic *Icecast) supervise() {
	for {
		_ = ic.cmd.Wait() // blocks until icecast dies
		time.Sleep(2 * time.Second)
		if err := ic.start(); err != nil {
			time.Sleep(5 * time.Second)
		}
	}
}

// readSourcePw extracts the <source-password> from an icecast.xml blob.
func readSourcePw(xml string) string {
	const tag = "<source-password>"
	i := indexOf(xml, tag)
	if i < 0 {
		return ""
	}
	rest := xml[i+len(tag):]
	j := indexOf(rest, "</source-password>")
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// readAdminPw extracts the <admin-password> from an icecast.xml blob.
func readAdminPw(xml string) string {
	const tag = "<admin-password>"
	i := indexOf(xml, tag)
	if i < 0 {
		return ""
	}
	rest := xml[i+len(tag):]
	j := indexOf(rest, "</admin-password>")
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// killStrayIcecast terminates any icecast not spawned by us, so our config
// (and its password) is the one bound to the port.
func killStrayIcecast() {
	_ = exec.Command("pkill", "-f", "icecast -c").Run()
	time.Sleep(500 * time.Millisecond)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// renderConfig builds an icecast.xml with a generous source-timeout (the
// persistent master has brief gaps between decoders that the default 10s
// timeout rejects) and a /stream.mp3 mount.
func renderConfig(stateDir, host string, port int, sourcePw, adminPw string) string {
	logs := filepath.Join(stateDir, "logs")
	return fmt.Sprintf(`<icecast>
  <location>Earth</location>
  <admin>radio-dj@localhost</admin>
  <limits>
    <clients>100</clients>
    <sources>4</sources>
    <source-timeout>120</source-timeout>
    <burst-size>65536</burst-size>
  </limits>
  <authentication>
    <source-password>%s</source-password>
    <relay-password>%s</relay-password>
    <admin-user>admin</admin-user>
    <admin-password>%s</admin-password>
  </authentication>
  <hostname>%s</hostname>
  <listen-socket><port>%d</port></listen-socket>
  <fileserve>1</fileserve>
  <paths>
    <basedir>%s/share/icecast</basedir>
    <logdir>%s</logdir>
    <webroot>%s/share/icecast/web</webroot>
    <adminroot>%s/share/icecast/admin</adminroot>
    <alias source="/" destination="/status.xsl"/>
  </paths>
  <logging><accesslog>access.log</accesslog><errorlog>error.log</errorlog><loglevel>2</loglevel></logging>
  <mount>
    <mount-name>/stream.mp3</mount-name>
    <burst-size>65536</burst-size>
    <stream-name>radio-dj</stream-name>
  </mount>
</icecast>
`, sourcePw, sourcePw, adminPw, host, port, brewPrefix(), logs, brewPrefix(), brewPrefix())
}

// brewPrefix returns the homebrew prefix for icecast's share dir (macOS).
// On non-brew systems the operator overrides via the config file.
func brewPrefix() string {
	if p, err := exec.LookPath("icecast"); err == nil {
		// /opt/homebrew/opt/icecast  or  /usr/local/opt/icecast
		dir := filepath.Dir(filepath.Dir(p))
		if _, err := os.Stat(filepath.Join(dir, "share", "icecast")); err == nil {
			return dir
		}
	}
	return "/opt/homebrew/opt/icecast"
}
