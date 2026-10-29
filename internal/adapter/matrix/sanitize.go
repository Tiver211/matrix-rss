package matrix

import (
	"regexp"

	"github.com/microcosm-cc/bluemonday"
)

// htmlPolicy allows the subset of HTML that the Matrix specification recommends for messages, minus
// images (they point to mxc:// URIs) and styling. Everything else, scripts included, is removed.
var htmlPolicy = newHTMLPolicy()

func newHTMLPolicy() *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowElements(
		"del", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "p", "ul", "ol", "sup", "sub", "li",
		"b", "i", "u", "strong", "em", "strike", "code", "hr", "br", "div", "table", "thead", "tbody",
		"tr", "th", "td", "caption", "pre", "span", "details", "summary",
	)
	policy.AllowAttrs("href").OnElements("a")
	policy.AllowElements("a")
	policy.AllowURLSchemes("http", "https", "ftp", "mailto", "magnet")
	policy.RequireNoFollowOnLinks(true)
	policy.AllowAttrs("start").Matching(regexp.MustCompile(`^[0-9]+$`)).OnElements("ol")
	policy.AllowAttrs("class").Matching(regexp.MustCompile(`^language-[a-zA-Z0-9_+-]+$`)).OnElements("code")
	return policy
}

// sanitizeHTML makes the formatted body of a message safe to show elsewhere.
func sanitizeHTML(html string) string {
	return htmlPolicy.Sanitize(html)
}
