package tui

import "github.com/charmbracelet/bubbles/key"

var (
	keyEnter    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open"))
	keyBack     = key.NewBinding(key.WithKeys("esc", "backspace", "left", "h"), key.WithHelp("esc", "back"))
	keyClose    = key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "close"))
	keyQuit     = key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit"))
	keyDownload = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "download"))
	keySearch   = key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "search"))
	keyWeb      = key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "open on Learn"))
	keyAll      = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "all courses"))
	keyReload   = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "reload"))
	keyNextTab  = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next tab"))
	keyPrevTab  = key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev tab"))
	keyTabN     = key.NewBinding(key.WithKeys("1", "2", "3", "4"), key.WithHelp("1-4", "tab"))
	keySaveAll  = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "save attachments"))
	keyOpenAtt  = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open attachment"))
	keyDetailBk = key.NewBinding(key.WithKeys("esc", "q", "backspace"), key.WithHelp("esc", "back"))
)
