package viewspec

// Standard returns a registry holding the built-in vocabulary. Each
// call returns a fresh copy, so one caller's registrations cannot leak
// into another's. Every widget lives in its own widget_*.go file; this
// table is the index.
func Standard() *Registry { return standard.clone() }

var standard = func() *Registry {
	r := NewRegistry()
	must(r.Extractor("lines", newLinesExtractor))
	must(r.Extractor("columns", newColumnsExtractor))
	must(r.Extractor("json", newJSONExtractor))
	must(r.Extractor("fixed", newFixedExtractor))
	must(r.Extractor("pairs", newPairsExtractor))
	must(r.Extractor("delimited", newDelimitedExtractor))
	must(r.Extractor("indent", newIndentExtractor))
	must(r.Extractor("prefix", newPrefixExtractor))
	must(r.Extractor("box", newBoxExtractor))
	must(r.Extractor("none", newNoneExtractor))
	must(r.Widget(RowKind, rowWidget{}))
	must(r.Widget(PanelKind, panelWidget{}))
	must(r.Widget("text", textWidget{}))
	must(r.Widget("table", tableWidget{}))
	must(r.Widget("list", listWidget{}))
	must(r.Widget("keyvalue", keyvalueWidget{}))
	must(r.Widget("meter", meterWidget{}))
	must(r.Widget("badges", badgesWidget{}))
	must(r.Widget("tree", treeWidget{}))
	must(r.Widget("sparkline", sparklineWidget{}))
	must(r.Widget("bar", barWidget{}))
	must(r.Widget("histogram", histogramWidget{}))
	must(r.Widget("gauge", gaugeWidget{}))
	must(r.Widget("stack", stackWidget{}))
	must(r.Widget("diverge", divergeWidget{}))
	must(r.Widget("scatter", scatterWidget{}))
	must(r.Widget("heatmap", heatmapWidget{}))
	must(r.Widget("stat", statWidget{}))
	must(r.Widget("dots", dotsWidget{}))
	must(r.Widget("flow", flowWidget{}))
	must(r.Widget("gantt", ganttWidget{}))
	must(r.Widget("timeline", timelineWidget{}))
	must(r.Widget("boxplot", boxplotWidget{}))
	must(r.Widget("series", seriesWidget{}))
	must(r.Widget("delta", deltaWidget{}))
	must(r.Widget("log", rawWidget{mode: "log"}))
	must(r.Widget("errors", rawWidget{mode: "errors"}))
	must(r.Widget("json", rawWidget{mode: "json"}))
	must(r.Widget("diff", rawWidget{mode: "diff"}))
	must(r.Widget("code", rawWidget{mode: "code"}))
	return r
}()

func must(err error) {
	if err != nil {
		panic(err)
	}
}
