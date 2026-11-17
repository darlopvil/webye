package main

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

// CeX product descriptions are HTML. Measured on real products, they only
// use p, ul, li and h4, plus html and body around the whole text, and no
// attributes at all. sanear keeps that structure and a few harmless inline
// tags, rebuilt from scratch without any attribute, so nothing from CeX can
// add a script, a style, a link or a request to a third party.

// permitidas maps each accepted tag to the tag that is written out.
var permitidas = map[string]string{
	"p": "p", "br": "br", "ul": "ul", "ol": "ol", "li": "li",
	"h1": "h4", "h2": "h4", "h3": "h4", "h4": "h4", "h5": "h4", "h6": "h4",
	"strong": "strong", "b": "strong", "em": "em", "i": "em",
}

var (
	reEtiqueta = regexp.MustCompile(`(?s)<!--.*?-->|<\s*(/?)\s*([a-zA-Z][a-zA-Z0-9]*)\b[^>]*>`)
	reEspacios = regexp.MustCompile(`\s+`)
	// Elements whose content is code, not text: removed with everything
	// inside them.
	reBloques []*regexp.Regexp
)

func init() {
	for _, e := range []string{"script", "style", "iframe", "object", "embed", "template", "noscript", "svg", "math"} {
		reBloques = append(reBloques, regexp.MustCompile(`(?is)<\s*`+e+`\b.*?<\s*/\s*`+e+`\s*>`))
	}
}

func sanear(entrada string) template.HTML {
	for _, re := range reBloques {
		entrada = re.ReplaceAllString(entrada, "")
	}
	var b strings.Builder
	var abiertas []string
	texto := func(s string) {
		s = reEspacios.ReplaceAllString(s, " ")
		b.WriteString(html.EscapeString(html.UnescapeString(s)))
	}

	resto := entrada
	for {
		m := reEtiqueta.FindStringSubmatchIndex(resto)
		if m == nil {
			texto(resto)
			break
		}
		texto(resto[:m[0]])
		cierre := m[2] >= 0 && resto[m[2]:m[3]] == "/"
		nombre := ""
		if m[4] >= 0 {
			nombre = permitidas[strings.ToLower(resto[m[4]:m[5]])]
		}
		resto = resto[m[1]:]

		switch {
		case nombre == "":
			// Comments and tags outside the list disappear; their text stays.
		case nombre == "br":
			b.WriteString("<br>")
		case !cierre:
			abiertas = append(abiertas, nombre)
			b.WriteString("<" + nombre + ">")
		default:
			// Close up to the matching open tag; a closing tag that was
			// never opened is dropped, so it cannot close webye's own page.
			for i := len(abiertas) - 1; i >= 0; i-- {
				if abiertas[i] == nombre {
					for j := len(abiertas) - 1; j >= i; j-- {
						b.WriteString("</" + abiertas[j] + ">")
					}
					abiertas = abiertas[:i]
					break
				}
			}
		}
	}
	for j := len(abiertas) - 1; j >= 0; j-- {
		b.WriteString("</" + abiertas[j] + ">")
	}
	return template.HTML(strings.TrimSpace(b.String()))
}
