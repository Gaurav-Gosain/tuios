package config

// MailAlertsConfig is the [notifications.mail] table: what tuios does when
// agent mail arrives for the person, or a notice goes to the whole session.
//
// Every key is a pointer, and a key left out follows the same key in
// [notifications.agent]. Mail alerts used that table before this one existed,
// so a config that does not name [notifications.mail] behaves as it did.
// Sound mode, the cooldown, the cue files and quiet hours always come from
// [notifications.agent]: one clock and one set of sounds for every alert.
type MailAlertsConfig struct {
	// Enabled is the switch for mail alerts. Unset follows
	// notifications.agent.enabled.
	Enabled *bool `toml:"enabled"`

	// Notify writes a desktop notification to the attached terminal. Unset
	// follows notifications.agent.notify.
	Notify *bool `toml:"notify"`

	// Sound makes a mail alert audible, the way notifications.agent.sound_mode
	// says. Unset follows notifications.agent.sound.
	Sound *bool `toml:"sound"`

	// Dock shows the alert in the dock, where a click opens the thread. Unset
	// follows notifications.agent.dock.
	Dock *bool `toml:"dock"`

	// BetweenAgents alerts on a message from one agent to another as well.
	// Such a message only counts on the recipient's rail row otherwise, since
	// it is the two agents' business. Default: false.
	BetweenAgents *bool `toml:"between_agents"`
}

// MailAlertPolicy is the policy a mail alert runs under: the agent policy with
// the [notifications.mail] keys laid over it. The embedded policy keeps its
// sound mode, cooldown, cue files and quiet hours.
type MailAlertPolicy struct {
	AgentAlertPolicy
	// BetweenAgents is true when a message between two agents alerts too.
	BetweenAgents bool
}

// ResolveMailAlerts lays the mail table over an already resolved agent
// policy. A nil table, or a key it leaves out, keeps the agent value.
func ResolveMailAlerts(c *MailAlertsConfig, agent AgentAlertPolicy) MailAlertPolicy {
	p := MailAlertPolicy{AgentAlertPolicy: agent}
	if c == nil {
		return p
	}
	boolOr := func(v *bool, def bool) bool {
		if v == nil {
			return def
		}
		return *v
	}
	p.Enabled = boolOr(c.Enabled, p.Enabled)
	p.Notify = boolOr(c.Notify, p.Notify)
	p.Sound = boolOr(c.Sound, p.Sound)
	p.Dock = boolOr(c.Dock, p.Dock)
	p.BetweenAgents = boolOr(c.BetweenAgents, false)
	return p
}
