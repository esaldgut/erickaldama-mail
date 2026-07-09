package render

import (
	"context"
	"strings"
	"testing"
)

func TestRenderImageDegradesWhenAbsent(t *testing.T) { // SAN-4
	// si chafa no está, ChafaAvailable() es false y RenderImage devuelve un placeholder, no panic
	if ChafaAvailable() {
		t.Skip("chafa present; this test covers the absent path")
	}
	out := RenderImage(context.Background(), []byte("anything"), 40, 20)
	if !strings.Contains(out, "chafa") {
		t.Errorf("absent chafa should mention install hint, got %q", out)
	}
}

func TestRenderImageInvalidData(t *testing.T) { // SAN-4b
	if !ChafaAvailable() {
		t.Skip("chafa absent; this test needs chafa to test the invalid-image path")
	}
	out := RenderImage(context.Background(), []byte("not an image"), 40, 20)
	if !strings.Contains(out, "no reconocida") && !strings.Contains(out, "inválid") {
		t.Errorf("invalid image should yield placeholder, got %q", out)
	}
}
