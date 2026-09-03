//go:build windows

package install

import (
	"fmt"
	"os/exec"
)

// Windows uses a per-user Task Scheduler entry, avoiding a service account and
// keeping music, config and credentials in the signed-in user's profile.
func installService(bin string) error {
	command := fmt.Sprintf(`"%s" serve`, bin)
	return exec.Command("schtasks", "/create", "/tn", "radio-dj", "/sc", "onlogon", "/tr", command, "/f").Run()
}

func uninstallService() error {
	return exec.Command("schtasks", "/delete", "/tn", "radio-dj", "/f").Run()
}

func postCopy(string) {}
