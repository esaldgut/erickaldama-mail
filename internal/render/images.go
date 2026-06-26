package render

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// ChafaAvailable reports whether the chafa binary is in PATH.
func ChafaAvailable() bool {
	_, err := exec.LookPath("chafa")
	return err == nil
}

// RenderImage renders image bytes to ANSI symbols (half-blocks) via chafa, sized to cols×rows cells.
// Uses -f symbols (survives tmux pane switches, no passthrough needed). Degrades to a placeholder on
// any failure (chafa absent, invalid image, timeout) — never panics. SAN-3: argv-slice + stdin, no sh -c.
func RenderImage(ctx context.Context, data []byte, cols, rows int) string {
	if !ChafaAvailable() {
		return "[chafa no instalado — brew install chafa para ver imágenes]"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	size := fmt.Sprintf("%dx%d", cols, rows)
	cmd := exec.CommandContext(ctx, "chafa", "--size", size, "-f", "symbols", "-")
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "[imagen no reconocida — formato inválido o vacío]" // SAN-4b
		}
		return "[error renderizando imagen]"
	}
	return string(out)
}
