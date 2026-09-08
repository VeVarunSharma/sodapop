package config

const currentPreferencesVersion = 3

type ModelSettings struct {
	ContextTier     string `json:"context_tier"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type Preferences struct {
	Version       int                      `json:"version"`
	Theme         string                   `json:"theme"`
	Model         string                   `json:"model,omitempty"`
	ModelSettings map[string]ModelSettings `json:"model_settings,omitempty"`
	ReducedMotion bool                     `json:"reduced_motion"`
	NoColor       bool                     `json:"no_color"`
	ASCII         bool                     `json:"ascii"`
	Personality   string                   `json:"personality"`
}

func DefaultPreferences() Preferences {
	return Preferences{
		Version:       currentPreferencesVersion,
		Theme:         "arcade",
		Personality:   "playful",
		ModelSettings: make(map[string]ModelSettings),
	}
}
