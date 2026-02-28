package voice

// ICEServerConfig is the JSON-friendly ICE server representation shared with clients.
type ICEServerConfig struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// Config defines voice runtime behavior.
type Config struct {
	SignalVersion          string
	MaxParticipantsPerRoom int
	ICEServers             []ICEServerConfig
}

func (c Config) Normalized() Config {
	out := c
	if out.SignalVersion == "" {
		out.SignalVersion = DefaultSignalVersion
	}
	if out.MaxParticipantsPerRoom <= 0 {
		out.MaxParticipantsPerRoom = 8
	}
	if len(out.ICEServers) == 0 {
		out.ICEServers = []ICEServerConfig{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		}
	}
	return out
}
