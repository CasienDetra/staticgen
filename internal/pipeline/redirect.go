package pipeline

import (
	"fmt"
	"html"
	"strconv"
	"strings"
)

const redirectTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Redirecting…</title>
<link rel="canonical" href="%s">
<meta http-equiv="refresh" content="0; url=%s">
<script>window.location.replace(%s);</script>
</head>
<body>
<p>This page has moved to <a href="%s">%s</a>.</p>
</body>
</html>
`

// normalizeAlias ensures an alias reads as a rooted path so the URL mapper can
// turn it into an output path.
func normalizeAlias(alias string) string {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return "/"
	}
	if !strings.HasPrefix(alias, "/") {
		alias = "/" + alias
	}
	return alias
}

func isAbsoluteURL(s string) bool {
	for _, prefix := range []string{"http://", "https://", "mailto:", "//"} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// redirectPage renders a stub that sends the browser to target.
//
// Both a meta refresh and a script redirect are emitted: the script covers
// modern browsers instantly, and the meta refresh covers clients with scripting
// disabled. The target is HTML-escaped for markup and separately quoted for the
// script so a URL can never break out of either context.
func redirectPage(target string) ([]byte, error) {
	if strings.TrimSpace(target) == "" {
		return nil, fmt.Errorf("redirect target is empty")
	}
	attr := html.EscapeString(target)
	text := html.EscapeString(target)
	script := jsString(target)
	page := fmt.Sprintf(redirectTemplate, attr, attr, script, attr, text)
	return []byte(page), nil
}

// jsString renders s as a JavaScript string literal. strconv.Quote handles the
// escapes; angle brackets are additionally encoded so a target containing
// "</script>" cannot terminate the script element early.
func jsString(s string) string {
	return strings.ReplaceAll(strconv.Quote(s), "<", "\\u003c")
}
