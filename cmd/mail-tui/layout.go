package main

// Layout frame sizes for the two-pane browse view. RoundedBorder consumes 2 cols
// (left+right) and 2 rows (top+bottom) per pane; the global footer takes 1 row.
const (
	frameW  = 2 // border left+right per pane
	frameH  = 2 // border top+bottom per pane
	footerH = 1 // global footer line
)

// panelDims computes the INNER dimensions (excluding borders) of the list and reader
// panes so that JoinHorizontal of both bordered panes fits exactly in termWidth, and
// each pane plus its border plus the footer fits in termHeight. The reader gets the
// remainder of the width so integer rounding never overflows (audit C1/C2).
func panelDims(termWidth, termHeight int) (listW, readerW, panelH int) {
	availW := termWidth - 2*frameW // discount the border of BOTH panes
	if availW < 2 {
		availW = 2
	}
	listW = availW * 35 / 100
	if listW < 1 {
		listW = 1
	}
	readerW = availW - listW // remainder to reader
	if readerW < 1 {
		readerW = 1
	}
	panelH = termHeight - frameH - footerH
	if panelH < 1 {
		panelH = 1
	}
	return listW, readerW, panelH
}
