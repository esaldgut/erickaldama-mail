package main

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

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

var (
	focusedBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.AdaptiveColor{Light: "63", Dark: "86"})
	blurredBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.AdaptiveColor{Light: "250", Dark: "240"})
	footerStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "240", Dark: "245"})
)

func renderTwoPane(m model) string {
	listW, readerW, panelH := panelDims(m.termWidth, m.termHeight)
	lb, rb := blurredBorder, blurredBorder
	if m.focus == focusList {
		lb = focusedBorder
	} else {
		rb = focusedBorder
	}
	left := lb.Width(listW).Height(panelH).Render(m.list.View())
	right := rb.Width(readerW).Height(panelH).Render(m.viewport.View())
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	var foot string
	if m.focus == focusList {
		foot = "/ filtrar · enter abrir · tab→reader · q salir"
	} else {
		foot = fmt.Sprintf("j/k scroll · i img · R remote · tab→lista · [%d%%]", int(m.viewport.ScrollPercent()*100))
	}
	if m.inflight > 0 {
		foot = m.spinner.View() + " " + foot
	}
	if m.statusMsg != "" {
		foot = m.statusMsg + " · " + foot
	}
	return body + "\n" + footerStyle.Render(foot)
}
