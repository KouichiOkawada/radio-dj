//go:build linux

package install

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
)

// postCopy is a no-op on Linux.
func postCopy(_ string) {}

// initSystem reports the service manager present on this host: "openrc",
// "systemd", or "" (unsupported). Detection is by marker binary + the
// openrc-run interpreter, not by /proc/1 — containers can run one init under
// another, and a bare "systemctl" on an OpenRC box (via a compat shim) would
// otherwise misroute.
func initSystem() string {
	if _, err := exec.LookPath("rc-service"); err == nil {
		if _, err := os.Stat("/sbin/openrc-run"); err == nil {
			return "openrc"
		}
	}
	if _, err := exec.LookPath("systemctl"); err == nil {
		return "systemd"
	}
	return ""
}

// serviceUser is the account radio-dj will run as. Under sudo/doas it is the
// invoking user (SUDO_USER/DOAS_USER), since OpenRC system services need root
// to install but must not run radio-dj as root.
func serviceUser() (string, error) {
	for _, k := range []string{"DOAS_USER", "SUDO_USER"} {
		if u := os.Getenv(k); u != "" {
			return u, nil
		}
	}
	if u := os.Getenv("USER"); u != "" && u != "root" {
		return u, nil
	}
	if usr, err := user.Current(); err == nil {
		return usr.Username, nil
	}
	return "", fmt.Errorf("could not determine service user")
}

func installService(bin string) error {
	switch initSystem() {
	case "openrc":
		return installOpenRC(bin)
	case "systemd":
		return installSystemd(bin)
	default:
		return fmt.Errorf("no supported init system found (need OpenRC's rc-service or systemd's systemctl)")
	}
}

func uninstallService() error {
	switch initSystem() {
	case "openrc":
		return uninstallOpenRC()
	case "systemd":
		return uninstallSystemd()
	default:
		return fmt.Errorf("no supported init system found")
	}
}

// --- systemd ---

// installSystemd writes a systemd user unit and enables it.
func installSystemd(bin string) error {
	unit, err := unitPath()
	if err != nil {
		return err
	}
	if err := os.WriteFile(unit, []byte(unitContent(bin)), 0o644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}
	// reload + enable + start
	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", "--now", label},
	} {
		if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %s: %w\n%s", strings.Join(args, " "), err, out)
		}
	}
	// enable-linger so the user service survives logout/reboot
	if out, err := exec.Command("loginctl", "enable-linger", os.Getenv("USER")).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "  (loginctl enable-linger failed — service will stop on logout:\n  %s)\n", strings.TrimSpace(string(out)))
	}
	return nil
}

// uninstallSystemd stops, disables, and removes the systemd user unit.
func uninstallSystemd() error {
	unit, err := unitPath()
	if err != nil {
		return err
	}
	// stop + disable (ignore errors if not loaded)
	_, _ = exec.Command("systemctl", "--user", "stop", label).CombinedOutput()
	_, _ = exec.Command("systemctl", "--user", "disable", label).CombinedOutput()
	_ = os.Remove(unit)
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "  (daemon-reload: %v)\n", err)
	}
	fmt.Printf("✓ radio-dj uninstalled (systemd %s removed)\n", label)
	return nil
}

func unitPath() (string, error) {
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, label+".service"), nil
}

// unitContent renders the systemd unit, baking envVars() into Environment= lines
// so the service can find ffmpeg/edge-tts and the API keys without a shell.
func unitContent(bin string) string {
	vars := envVars()
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var envLines []string
	for _, k := range keys {
		envLines = append(envLines, fmt.Sprintf(`Environment="%s=%s"`, k, vars[k]))
	}
	sd := stateDir()
	return fmt.Sprintf(`[Unit]
Description=radio-dj — 24/7 AI DJ radio
After=network.target

[Service]
Type=simple
ExecStart=%s serve
Restart=always
RestartSec=5
%s
StandardOutput=append:%s/radio-dj.out.log
StandardError=append:%s/radio-dj.err.log

[Install]
WantedBy=default.target
`, bin, strings.Join(envLines, "\n"), sd, sd)
}

// --- OpenRC (Alpine, Artix, Void, Gentoo) ---
//
// Unlike the systemd/launchd paths (user services, no root), OpenRC has no
// native user-service concept, so this installs a SYSTEM service in
// /etc/init.d that drops privileges to the invoking user via command_user.
// Writing /etc/init.d + /etc/conf.d + rc-update therefore requires root; when
// the installer is not root it prints the exact escalation command to re-run
// with (sudo or doas).

const (
	openrcUnitPath = "/etc/init.d/radio-dj"
	openrcConfPath = "/etc/conf.d/radio-dj"
)

func installOpenRC(bin string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("OpenRC services are system-level and require root.\n" +
			"Re-run with escalation:\n" +
			"  sudo radio-dj install\n" +
			"  doas radio-dj install")
	}
	su, err := serviceUser()
	if err != nil {
		return err
	}
	usr, err := user.Lookup(su)
	if err != nil {
		return fmt.Errorf("lookup user %s: %w", su, err)
	}
	home := usr.HomeDir

	if err := os.WriteFile(openrcUnitPath, []byte(openrcUnitContent(bin, su, home)), 0o755); err != nil {
		return fmt.Errorf("write %s: %w", openrcUnitPath, err)
	}
	if err := os.WriteFile(openrcConfPath, []byte(openrcConfContent(home)), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", openrcConfPath, err)
	}
	if out, err := exec.Command("rc-update", "add", "radio-dj", "default").CombinedOutput(); err != nil {
		return fmt.Errorf("rc-update add: %w\n%s", err, out)
	}
	// start now, best-effort — ignore failure if the ports are already in use
	// (e.g. a foreground `radio-dj serve` still running). Boot starts still work.
	if out, err := exec.Command("rc-service", "radio-dj", "restart").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "  (rc-service restart: %v\n  %s)\n", err, strings.TrimSpace(string(out)))
	}
	fmt.Printf("✓ OpenRC service installed (%s)\n", openrcUnitPath)
	return nil
}

func uninstallOpenRC() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("OpenRC uninstall requires root.\nRe-run with escalation:\n  sudo radio-dj uninstall\n  doas radio-dj uninstall")
	}
	_, _ = exec.Command("rc-service", "radio-dj", "stop").CombinedOutput()
	_, _ = exec.Command("rc-update", "del", "radio-dj", "default").CombinedOutput()
	_ = os.Remove(openrcUnitPath)
	_ = os.Remove(openrcConfPath)
	fmt.Printf("✓ radio-dj uninstalled (OpenRC %s + conf.d removed)\n", label)
	return nil
}

// openrcUnitContent renders the OpenRC init.d script. command_background="yes"
// makes OpenRC background the (foreground) `radio-dj serve` and track it via
// pidfile; command_user drops privileges to the invoking user; directory sets
// the cwd. Env (HOME/PATH/RDJ_*/ZAI) is exported from /etc/conf.d/radio-dj.
func openrcUnitContent(bin, svcUser, home string) string {
	return fmt.Sprintf(`#!/sbin/openrc-run

name="radio-dj"
description="radio-dj — 24/7 AI DJ radio"
command="%s"
command_args="serve"
command_user="%s"
command_background="yes"
directory="%s"
pidfile="/run/${RC_SVCNAME}.pid"
output_log="%s/.radio-dj/radio-dj.out.log"
error_log="%s/.radio-dj/radio-dj.err.log"

depend() {
	need net
	after firewall
}
`, bin, svcUser, home, home, home)
}

// openrcConfContent renders /etc/conf.d/radio-dj. Everything is `export`ed so
// start-stop-daemon passes it to the child (bare assignments stay shell-local).
// HOME lets radio-dj find ~/.radio-dj/config.json (--chuid sets uid, not env).
// PATH is PREPENDED (:$PATH), never replaced — replacing it dropped /sbin,
// where start-stop-daemon lives, and broke the service on the first try.
func openrcConfContent(home string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# radio-dj OpenRC service config (generated by `radio-dj install`)\n")
	fmt.Fprintf(&b, "export HOME=\"%s\"\n", home)
	fmt.Fprintf(&b, "export PATH=\"%s/.local/bin:%s/.radio-dj/venv/bin:$PATH\"\n", home, home)
	vars := envVars()
	keys := make([]string, 0, len(vars))
	for k := range vars {
		if strings.HasPrefix(k, "RDJ_") || k == "ZAI_API_KEY" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "export %s=\"%s\"\n", k, vars[k])
	}
	return b.String()
}
