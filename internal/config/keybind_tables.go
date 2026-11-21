package config

// Table names one fixed keybinding table of config.toml. It indexes
// keybindTables and the registry's resolved sections, so a lookup by table
// is an array read.
type Table uint8

// The fixed keybinding tables, in the order validation and the defaults walk
// them.
const (
	TableWindowManagement Table = iota
	TableWorkspaces
	TableLayout
	TableModeControl
	TableSystem
	TableNavigation
	TableRestoreMinimized
	TablePrefixMode
	TableWindowPrefix
	TableMinimizePrefix
	TableWorkspacePrefix
	TableDebugPrefix
	TableTapePrefix
	TableLayoutPrefix
	TableTuiosPrefix
	TableTerminalMode
	TableSidebar
	TableSidebarFiles
	TableSidebarAgents
	TableQuickLook
	TableQuickLookInside
	TableInbox
	TableInboxPeek
	TableMail
	TableTransfers
	TableCopyMode
	TableReview
	TableGUI
	TableGlobal
	TableScript
	TableLists

	tableCount
)

type keybindTable struct {
	name  string
	field func(*KeybindingsConfig) *map[string][]string
}

var keybindTables = [tableCount]keybindTable{
	TableWindowManagement: {SectionWindowManagement, func(k *KeybindingsConfig) *map[string][]string { return &k.WindowManagement }},
	TableWorkspaces:       {SectionWorkspaces, func(k *KeybindingsConfig) *map[string][]string { return &k.Workspaces }},
	TableLayout:           {SectionLayout, func(k *KeybindingsConfig) *map[string][]string { return &k.Layout }},
	TableModeControl:      {SectionModeControl, func(k *KeybindingsConfig) *map[string][]string { return &k.ModeControl }},
	TableSystem:           {SectionSystem, func(k *KeybindingsConfig) *map[string][]string { return &k.System }},
	TableNavigation:       {SectionNavigation, func(k *KeybindingsConfig) *map[string][]string { return &k.Navigation }},
	TableRestoreMinimized: {SectionRestoreMinimized, func(k *KeybindingsConfig) *map[string][]string { return &k.RestoreMinimized }},
	TablePrefixMode:       {SectionPrefixMode, func(k *KeybindingsConfig) *map[string][]string { return &k.PrefixMode }},
	TableWindowPrefix:     {SectionWindowPrefix, func(k *KeybindingsConfig) *map[string][]string { return &k.WindowPrefix }},
	TableMinimizePrefix:   {SectionMinimizePrefix, func(k *KeybindingsConfig) *map[string][]string { return &k.MinimizePrefix }},
	TableWorkspacePrefix:  {SectionWorkspacePrefix, func(k *KeybindingsConfig) *map[string][]string { return &k.WorkspacePrefix }},
	TableDebugPrefix:      {SectionDebugPrefix, func(k *KeybindingsConfig) *map[string][]string { return &k.DebugPrefix }},
	TableTapePrefix:       {SectionTapePrefix, func(k *KeybindingsConfig) *map[string][]string { return &k.TapePrefix }},
	TableLayoutPrefix:     {SectionLayoutPrefix, func(k *KeybindingsConfig) *map[string][]string { return &k.LayoutPrefix }},
	TableTuiosPrefix:      {SectionTuiosPrefix, func(k *KeybindingsConfig) *map[string][]string { return &k.TuiosPrefix }},
	TableTerminalMode:     {SectionTerminalMode, func(k *KeybindingsConfig) *map[string][]string { return &k.TerminalMode }},
	TableSidebar:          {SectionSidebar, func(k *KeybindingsConfig) *map[string][]string { return &k.Sidebar }},
	TableSidebarFiles:     {SectionSidebarFiles, func(k *KeybindingsConfig) *map[string][]string { return &k.SidebarFiles }},
	TableSidebarAgents:    {SectionSidebarAgents, func(k *KeybindingsConfig) *map[string][]string { return &k.SidebarAgents }},
	TableQuickLook:        {SectionQuickLook, func(k *KeybindingsConfig) *map[string][]string { return &k.QuickLook }},
	TableQuickLookInside:  {SectionQuickLookInside, func(k *KeybindingsConfig) *map[string][]string { return &k.QuickLookInside }},
	TableInbox:            {SectionInbox, func(k *KeybindingsConfig) *map[string][]string { return &k.Inbox }},
	TableInboxPeek:        {SectionInboxPeek, func(k *KeybindingsConfig) *map[string][]string { return &k.InboxPeek }},
	TableMail:             {SectionMail, func(k *KeybindingsConfig) *map[string][]string { return &k.Mail }},
	TableTransfers:        {SectionTransfers, func(k *KeybindingsConfig) *map[string][]string { return &k.Transfers }},
	TableCopyMode:         {SectionCopyMode, func(k *KeybindingsConfig) *map[string][]string { return &k.CopyMode }},
	TableReview:           {SectionReview, func(k *KeybindingsConfig) *map[string][]string { return &k.Review }},
	TableGUI:              {SectionGUI, func(k *KeybindingsConfig) *map[string][]string { return &k.GUI }},
	TableGlobal:           {SectionGlobal, func(k *KeybindingsConfig) *map[string][]string { return &k.Global }},
	TableScript:           {SectionScript, func(k *KeybindingsConfig) *map[string][]string { return &k.Script }},
	TableLists:            {SectionLists, func(k *KeybindingsConfig) *map[string][]string { return &k.Lists }},
}
