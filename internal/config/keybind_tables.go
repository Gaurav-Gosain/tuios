package config

// keybindTable is one keybinding table of config.toml: its name there and the
// KeybindingsConfig field that holds it.
type keybindTable struct {
	name  string
	field func(*KeybindingsConfig) *map[string][]string
}

// keybindTables is every fixed keybinding table, once. The lookup by name, the
// defaults fill, the nil-map setup and validation all walk it, so a new table
// is added here and in the struct, and nowhere else. Declared menus are
// tables too, but they are named by the user and kept in Menus.
var keybindTables = [...]keybindTable{
	{SectionWindowManagement, func(k *KeybindingsConfig) *map[string][]string { return &k.WindowManagement }},
	{SectionWorkspaces, func(k *KeybindingsConfig) *map[string][]string { return &k.Workspaces }},
	{SectionLayout, func(k *KeybindingsConfig) *map[string][]string { return &k.Layout }},
	{SectionModeControl, func(k *KeybindingsConfig) *map[string][]string { return &k.ModeControl }},
	{SectionSystem, func(k *KeybindingsConfig) *map[string][]string { return &k.System }},
	{SectionNavigation, func(k *KeybindingsConfig) *map[string][]string { return &k.Navigation }},
	{SectionRestoreMinimized, func(k *KeybindingsConfig) *map[string][]string { return &k.RestoreMinimized }},
	{SectionPrefixMode, func(k *KeybindingsConfig) *map[string][]string { return &k.PrefixMode }},
	{SectionWindowPrefix, func(k *KeybindingsConfig) *map[string][]string { return &k.WindowPrefix }},
	{SectionMinimizePrefix, func(k *KeybindingsConfig) *map[string][]string { return &k.MinimizePrefix }},
	{SectionWorkspacePrefix, func(k *KeybindingsConfig) *map[string][]string { return &k.WorkspacePrefix }},
	{SectionDebugPrefix, func(k *KeybindingsConfig) *map[string][]string { return &k.DebugPrefix }},
	{SectionTapePrefix, func(k *KeybindingsConfig) *map[string][]string { return &k.TapePrefix }},
	{SectionLayoutPrefix, func(k *KeybindingsConfig) *map[string][]string { return &k.LayoutPrefix }},
	{SectionTuiosPrefix, func(k *KeybindingsConfig) *map[string][]string { return &k.TuiosPrefix }},
	{SectionTerminalMode, func(k *KeybindingsConfig) *map[string][]string { return &k.TerminalMode }},
	{SectionSidebar, func(k *KeybindingsConfig) *map[string][]string { return &k.Sidebar }},
	{SectionSidebarFiles, func(k *KeybindingsConfig) *map[string][]string { return &k.SidebarFiles }},
	{SectionSidebarAgents, func(k *KeybindingsConfig) *map[string][]string { return &k.SidebarAgents }},
	{SectionQuickLook, func(k *KeybindingsConfig) *map[string][]string { return &k.QuickLook }},
	{SectionQuickLookInside, func(k *KeybindingsConfig) *map[string][]string { return &k.QuickLookInside }},
	{SectionInbox, func(k *KeybindingsConfig) *map[string][]string { return &k.Inbox }},
	{SectionInboxPeek, func(k *KeybindingsConfig) *map[string][]string { return &k.InboxPeek }},
	{SectionMail, func(k *KeybindingsConfig) *map[string][]string { return &k.Mail }},
	{SectionTransfers, func(k *KeybindingsConfig) *map[string][]string { return &k.Transfers }},
	{SectionCopyMode, func(k *KeybindingsConfig) *map[string][]string { return &k.CopyMode }},
	{SectionReview, func(k *KeybindingsConfig) *map[string][]string { return &k.Review }},
	{SectionGUI, func(k *KeybindingsConfig) *map[string][]string { return &k.GUI }},
	{SectionGlobal, func(k *KeybindingsConfig) *map[string][]string { return &k.Global }},
	{SectionScript, func(k *KeybindingsConfig) *map[string][]string { return &k.Script }},
	{SectionLists, func(k *KeybindingsConfig) *map[string][]string { return &k.Lists }},
}
