package web

import (
	"bytes"
	htmlesc "html"
	"html/template"
	"strings"

	nethtml "golang.org/x/net/html"
)

var allowedTags = map[string]bool{
	"a":          true,
	"b":          true,
	"blockquote": true,
	"br":         true,
	"code":       true,
	"div":        true,
	"em":         true,
	"h1":         true,
	"h2":         true,
	"h3":         true,
	"h4":         true,
	"h5":         true,
	"h6":         true,
	"hr":         true,
	"i":          true,
	"img":        true,
	"li":         true,
	"ol":         true,
	"p":          true,
	"pre":        true,
	"span":       true,
	"strong":     true,
	"table":      true,
	"tbody":      true,
	"thead":      true,
	"tfoot":      true,
	"tr":         true,
	"td":         true,
	"th":         true,
	"colgroup":   true,
	"col":        true,
	"ul":         true,
}

func sanitizeHTML(input string) template.HTML {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	node, err := nethtml.Parse(strings.NewReader(input))
	if err != nil {
		return template.HTML(htmlesc.EscapeString(input))
	}

	body := findBody(node)
	var buf bytes.Buffer
	for child := body.FirstChild; child != nil; child = child.NextSibling {
		renderSanitized(&buf, child)
	}
	return template.HTML(buf.String())
}

func findBody(n *nethtml.Node) *nethtml.Node {
	if n.Type == nethtml.ElementNode && n.Data == "body" {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := findBody(child); found != nil {
			return found
		}
	}
	return n
}

func renderSanitized(buf *bytes.Buffer, n *nethtml.Node) {
	switch n.Type {
	case nethtml.TextNode:
		buf.WriteString(htmlesc.EscapeString(n.Data))
	case nethtml.ElementNode:
		tag := strings.ToLower(n.Data)
		if !allowedTags[tag] {
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				renderSanitized(buf, child)
			}
			return
		}
		buf.WriteString("<")
		buf.WriteString(tag)
		for _, attr := range n.Attr {
			key := strings.ToLower(attr.Key)
			val := strings.TrimSpace(attr.Val)
			if val == "" {
				continue
			}
			if !isAllowedAttr(tag, key) {
				continue
			}
			switch key {
			case "href":
				if !isSafeHref(val) {
					continue
				}
			case "src":
				if !isSafeImageSrc(val) {
					continue
				}
			case "style":
				val = sanitizeStyle(val)
				if val == "" {
					continue
				}
			case "width", "height", "border", "cellpadding", "cellspacing":
				val = sanitizeNumber(val)
				if val == "" {
					continue
				}
			}
			buf.WriteString(" ")
			buf.WriteString(key)
			buf.WriteString("=\"")
			buf.WriteString(htmlesc.EscapeString(val))
			buf.WriteString("\"")
		}
		if tag == "br" || tag == "hr" {
			buf.WriteString(" />")
			return
		}
		if tag == "img" {
			buf.WriteString(" />")
			return
		}
		buf.WriteString(">")
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			renderSanitized(buf, child)
		}
		buf.WriteString("</")
		buf.WriteString(tag)
		buf.WriteString(">")
	default:
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			renderSanitized(buf, child)
		}
	}
}

func isSafeHref(href string) bool {
	if href == "" {
		return false
	}
	lower := strings.ToLower(href)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "mailto:")
}

func isSafeImageSrc(src string) bool {
	if src == "" {
		return false
	}
	lower := strings.ToLower(src)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func isAllowedAttr(tag, key string) bool {
	switch key {
	case "style":
		return true
	case "title":
		return true
	}
	switch tag {
	case "a":
		return key == "href" || key == "title"
	case "img":
		return key == "src" || key == "alt" || key == "title" || key == "width" || key == "height"
	case "table":
		return key == "width" || key == "border" || key == "cellpadding" || key == "cellspacing" || key == "align"
	case "td", "th":
		return key == "width" || key == "height" || key == "align" || key == "valign" || key == "colspan" || key == "rowspan"
	case "tr", "tbody", "thead", "tfoot", "col", "colgroup", "div", "span", "p":
		return key == "align" || key == "valign"
	default:
		return false
	}
}

func sanitizeStyle(value string) string {
	val := strings.ToLower(strings.TrimSpace(value))
	if val == "" {
		return ""
	}
	if strings.Contains(val, "expression") || strings.Contains(val, "url(") {
		return ""
	}
	for _, r := range val {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ':' || r == ';' || r == '#' || r == '.' || r == '%' || r == ' ' || r == '-' || r == '(' || r == ')' || r == ',' {
			continue
		}
		return ""
	}
	return strings.TrimSpace(value)
}

func sanitizeNumber(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var out []rune
	for _, r := range value {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return string(out)
}
