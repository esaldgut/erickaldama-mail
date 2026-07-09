package message

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

// SanitizeResult carries the cleaned HTML plus the cid: refs found and the remote URLs blocked.
type SanitizeResult struct {
	HTML           string
	CIDRefs        []string
	BlockedRemotes []string
}

const remotePlaceholder = "[imagen remota bloqueada]"

// isRemoteURL reports whether a URL points to the network: http(s), or protocol-relative (//host).
// Case-insensitive (P-2: HTTP:// bypass). Covers the SAN-2 bypasses the audit found.
func isRemoteURL(v string) bool {
	lv := strings.ToLower(strings.TrimSpace(v))
	return strings.HasPrefix(lv, "http://") || strings.HasPrefix(lv, "https://") || strings.HasPrefix(lv, "//")
}

// SanitizeHTML cleans untrusted email HTML. With allowRemote=false (default), <img> with a remote src
// (http/https/protocol-relative, any case) is replaced by a text placeholder (anti tracking-pixel);
// cid: images survive. The HARD invariant SAN-2 is enforced by bluemonday restricting img.src to the
// cid: scheme via regexp (P-2) — NOT by the fragile manual HasPrefix. Pass 1 only collects refs and
// inserts the UX placeholder; bluemonday is the security boundary.
func SanitizeHTML(rawHTML string, allowRemote bool) (SanitizeResult, error) {
	var res SanitizeResult

	// Pass 1: preprocess with x/net/html — collect cid:/remotes, insert placeholder for blocked remotes (UX).
	root, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return res, err
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" {
			for _, a := range n.Attr {
				if a.Key != "src" {
					continue
				}
				switch {
				case strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Val)), "cid:"):
					res.CIDRefs = append(res.CIDRefs, strings.TrimPrefix(strings.TrimSpace(a.Val), "cid:"))
				case isRemoteURL(a.Val):
					res.BlockedRemotes = append(res.BlockedRemotes, a.Val)
					if !allowRemote {
						// P-8: turn <img> into a <span>[placeholder] WITHOUT corrupting the tree.
						n.Data = "span"
						n.DataAtom = 0
						n.Attr = nil
						n.AppendChild(&html.Node{Type: html.TextNode, Data: remotePlaceholder})
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	var buf bytes.Buffer
	if err := html.Render(&buf, root); err != nil {
		return res, err
	}

	// Pass 2: bluemonday IS the security boundary. Base UGCPolicy + restrict img.src to cid: via regexp.
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("alt", "width", "height").OnElements("img")
	if allowRemote {
		// escape consciente: allow http/https/cid in img.src
		p.AllowAttrs("src").Matching(regexp.MustCompile(`(?i)^(cid:|https?://|//)`)).OnElements("img")
		p.AllowURLSchemes("cid", "http", "https")
	} else {
		// P-2 HARD: only cid: survives in img.src — any other scheme/relative is stripped by bluemonday.
		p.AllowAttrs("src").Matching(regexp.MustCompile(`(?i)^cid:`)).OnElements("img")
		p.AllowURLSchemes("cid")
	}
	res.HTML = p.Sanitize(buf.String())
	return res, nil
}
