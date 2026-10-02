// Package ui is the full-screen TUI: an output pane and a history
// pane over an input bar. It subscribes to the bus and publishes
// intents back, so it imports no harness package at all.
//
// # What lives where
//
// A prefix groups files that share a role. A plain noun names a file
// that is the only one doing its job.
//
//	model.go        Model, New, Update: the loop everything hangs off
//	model_state.go  the small values Model is composed of
//	model_turn.go   turnBlock and callRow, what history is a list of
//
//	facts.go        facts in: the one place Model learns anything
//	intents.go      intents out: the one place it asks for anything
//	keys.go         which pane owns a keystroke
//	nav.go          what a keystroke moves
//
//	prompt.go       the input box and its slash dropdown
//	slash.go        the slash registry
//	panel.go        the /usage, /status and /help pages
//	undo.go         the undo question
//	forget.go       the /delete question
//	signin.go       a server asking to be signed in to
//
//	spec.go         viewspec wiring: registry, binding, fallbacks
//	spec_paint.go   viewspec's Painter, over lipgloss
//	styles.go       every style baked from the theme
//
//	view.go         composes the screen
//	view_layout.go  sizing
//	view_chrome.go  bars and pane headers
//	view_history.go the history pane
//	view_detail.go  the output pane
//	view_ask.go     the question boxes
//
// # What leaves this package
//
// A thing moves to a subpackage when it stops needing Model. That is
// the whole rule, and it is why island, layout, markdown, status,
// theme and welcome are subpackages while everything above is not:
// they take values and return strings. The compiler enforces it,
// since a subpackage importing ui would be an import cycle.
//
// Rendering could go the same way, but only after Model's state is
// passed to it as values. Worth doing if a second front-end ever
// wants the same drawing, not for one.
//
// # Naming
//
// A render method's suffix says what it returns, so a call site never
// has to go and look:
//
//	xxxLines   []string   pane content, one entry per row
//	xxxBar     string     a full-width strip (sessionBar, statusBar)
//	xxxBox     string     the bottom zone while it is asking
//	xxxHeader  string     a pane's title row
//
// Those name producers. Something that takes lines and gives lines
// back is a transform and keeps a verb: railed, wrapPlain, wrapStyled.
//
// State structs end in State (navState, undoState). An enum is named
// for what it enumerates (mode, entry, focusPane, keyOwner) and
// its constants all carry the type's stem, so modeConfirm, entryShell,
// ownerBusy and panelUsage each say what they belong to.
package ui
