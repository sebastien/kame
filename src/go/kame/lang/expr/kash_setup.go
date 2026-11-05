package expr

// SetupKey classifies the name of one Kash process setup entry.
type SetupKey int

const (
	SetupInvalid SetupKey = iota
	// SetupEnvEntry assigns one per-stage environment entry: NAME VALUE.
	SetupEnvEntry
	SetupEnvRecord
	SetupCwd
	SetupTimeout
	SetupAsync
	SetupAcceptExit
)

// ClassifySetupKey is the single stage-setup vocabulary shared by the parser,
// lowering, evaluator, and with-scope validation. Unknown names are invalid;
// any valid environment name selects SetupEnvEntry.
func ClassifySetupKey(key string) (SetupKey, bool) {
	switch key {
	case "env":
		return SetupEnvRecord, true
	case "cwd":
		return SetupCwd, true
	case "timeout":
		return SetupTimeout, true
	case "async":
		return SetupAsync, true
	case "accept-exit":
		return SetupAcceptExit, true
	}
	if !ValidEnvironmentName(key) {
		return SetupInvalid, false
	}
	return SetupEnvEntry, true
}

// ValidEnvironmentName is the environment-record key grammar, deliberately
// separate from the setup vocabulary (which also reserves dash-named keys).
func ValidEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		b := name[i]
		if b == '_' || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (i > 0 && b >= '0' && b <= '9') {
			continue
		}
		return false
	}
	return true
}
