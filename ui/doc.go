// Package ui is the full-screen TUI: an output pane and a history
// pane over an input bar. It subscribes to the bus and publishes
// intents back, so it imports no harness package at all.
//
// The files, in the order it helps to read them:
//
//	ui.go          Model, New, Update — the loop everything hangs off
//	state.go       the small values Model is composed of
//	blocks.go      turnBlock and callRow, what history is a list of
//	apply.go       facts in: the one place Model learns anything
//	intents.go     intents out: the one place it asks for anything
//	keys.go        which pane owns a keystroke
//	nav.go         what a keystroke moves
//	prompt.go      the input box and its slash dropdown
//	slash.go       the slash registry
//	panel.go       /usage, /status, /help
//	undo.go        the undo question
//	spec.go        viewspec wiring: registry, binding, fallbacks
//	spec_paint.go  viewspec's Painter, implemented over lipgloss
//	styles.go      every style baked from the theme
//	view_*.go      rendering, and nothing else
//
// Rendering lives in view_*.go because Go keeps a method in its
// receiver's package: those are all func (m Model), so a subpackage
// would need Model's state passed as values first.
package ui
