package tui

import "os"

func (m *model) restoreCopyModeEnvIfNeeded() {
	if m.installCopyModeHadPrev {
		_ = os.Setenv("CSM_COPY_MODE", m.installCopyModePrev)
	} else {
		_ = os.Unsetenv("CSM_COPY_MODE")
	}
	m.installCopyModeHadPrev = false
	m.installCopyModePrev = ""
}

func (m *model) restoreSteamValidateEnvIfNeeded() {
	if m.installValidateHadPrev {
		_ = os.Setenv("CSM_STEAMCMD_VALIDATE", m.installValidatePrev)
	} else {
		_ = os.Unsetenv("CSM_STEAMCMD_VALIDATE")
	}
	m.installValidateHadPrev = false
	m.installValidatePrev = ""
}
