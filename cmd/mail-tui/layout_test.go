package main

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestPanelDimsFitWidth(t *testing.T) {
	// El JoinHorizontal de los 2 paneles (cada uno con RoundedBorder) NO debe exceder termWidth.
	for _, tw := range []int{80, 100, 120, 132} {
		th := 40
		listW, readerW, panelH := panelDims(tw, th)
		// Renderiza ambos paneles con RoundedBorder al ancho interno calculado y únelos.
		border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
		left := border.Width(listW).Height(panelH).Render("")
		right := border.Width(readerW).Height(panelH).Render("")
		join := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
		if got := lipgloss.Width(join); got != tw {
			t.Errorf("tw=%d: JoinHorizontal width=%d, want %d (desborde=%d)", tw, got, tw, got-tw)
		}
	}
}

func TestPanelDimsFitHeight(t *testing.T) {
	tw, th := 100, 40
	_, _, panelH := panelDims(tw, th)
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	rendered := border.Width(50).Height(panelH).Render("")
	// border top+bottom (2) + footer (1) deben caber en th: altura del panel renderizado <= th-footerH.
	if got := lipgloss.Height(rendered); got > th-footerH {
		t.Errorf("panel height %d exceeds th-footer %d", got, th-footerH)
	}
}
