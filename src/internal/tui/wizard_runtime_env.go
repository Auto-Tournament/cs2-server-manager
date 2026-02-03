package tui

import (
	"os"

	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
)

// applyWizardRuntimeEnv sets process env vars that affect long-running install
// operations (SteamCMD + master→server replication). Values are restored when
// the wizard finishes/fails/cancels via restore* helpers.
func (m *model) applyWizardRuntimeEnv() {
	// Copy mode: fastCopy => auto, else legacy.
	if prev, ok := os.LookupEnv("CSM_COPY_MODE"); ok {
		m.installCopyModeHadPrev = true
		m.installCopyModePrev = prev
	} else {
		m.installCopyModeHadPrev = false
		m.installCopyModePrev = ""
	}
	if m.wizard.cfg.fastCopy {
		_ = os.Setenv("CSM_COPY_MODE", "auto")
	} else {
		_ = os.Setenv("CSM_COPY_MODE", "legacy")
	}

	// SteamCMD validate: safe => 1, fast => 0.
	if prev, ok := os.LookupEnv("CSM_STEAMCMD_VALIDATE"); ok {
		m.installValidateHadPrev = true
		m.installValidatePrev = prev
	} else {
		m.installValidateHadPrev = false
		m.installValidatePrev = ""
	}
	if m.wizard.cfg.steamcmdValidate {
		_ = os.Setenv("CSM_STEAMCMD_VALIDATE", "1")
	} else {
		_ = os.Setenv("CSM_STEAMCMD_VALIDATE", "0")
	}

	// Reset copy stats so the final wizard summary reflects this install run.
	csm.ResetCopyStats()
}
