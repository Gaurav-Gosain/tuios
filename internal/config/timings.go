package config

// The defaults of the timing options that have no environment variable. Each
// is the value the code used before it was an option.
const (
	DefaultSaveSeconds                 = 30
	DefaultExecTimeoutSeconds          = 30
	DefaultHostHeartbeatSeconds        = 15
	DefaultHostHeartbeatTimeoutSeconds = 5
	DefaultHostCallTimeoutSeconds      = 8
	DefaultReconnectSeconds            = 15
	DefaultAskWaitSeconds              = 120
	DefaultUndoWindowSeconds           = 10
)
