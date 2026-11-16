package repo

// PushSettings is the effective set of push options after CLI and config are merged.
type PushSettings struct {
	Repos    []string
	Model    string
	DryRun   bool
	Yes      bool
	Continue bool
	Abort    bool
}

// PullSettings is the effective set of pull options after CLI and config are merged.
type PullSettings struct {
	Repos  []string
	Delete bool
}

// ResolvePushSettings overlays config defaults onto CLI values. A field is
// taken from config only when the corresponding flag was not set on the
// command line, so explicit CLI arguments always win.
func ResolvePushSettings(cliSettings PushSettings, config *Config, explicit map[string]bool) PushSettings {
	resolved := cliSettings
	if len(resolved.Repos) == 0 && config.Patch != nil {
		resolved.Repos = config.Patch.Repos
	}
	if !explicit["m"] && config.Push.Model != "" {
		resolved.Model = config.Push.Model
	}
	if !explicit["dry-run"] {
		resolved.DryRun = config.Push.DryRun
	}
	if !explicit["yes"] {
		resolved.Yes = config.Push.Yes
	}
	if !explicit["continue"] {
		resolved.Continue = config.Push.Continue
	}
	if !explicit["abort"] {
		resolved.Abort = config.Push.Abort
	}
	return resolved
}

// ResolvePullSettings overlays config defaults onto CLI values using the same
// explicit-flag precedence rule as ResolvePushSettings.
func ResolvePullSettings(cliSettings PullSettings, config *Config, explicit map[string]bool) PullSettings {
	resolved := cliSettings
	if len(resolved.Repos) == 0 && config.Patch != nil {
		resolved.Repos = config.Patch.Repos
	}
	if !explicit["d"] {
		resolved.Delete = config.Pull.Delete
	}
	return resolved
}
