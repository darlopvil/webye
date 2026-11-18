package main

import (
	"cmp"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Filters work like the CeX website's: the same Algolia parameters and the
// same URL parameters, so a filtered CeX link opens the same filtered
// search here.
//
//	stores=A~B               facetFilters [["stores:A","stores:B"]]: A or B
//	categoryFriendlyName=…   one group per list; groups add up: A and B
//	Clasificación PEGI=18+   any product attribute, by its own name
//	sellPrice=10:20          numericFilters ["sellPrice>=10","sellPrice<=20"];
//	                         "15:" and ":20" are open ranges
//	availability=allStock    also shows sold-out products

const (
	atributoTiendas   = "stores"
	atributoCategoria = "categoryFriendlyName"
	separadorValores  = "~"

	// The richest list of filters among the searches probed (ps4): 11
	// product attributes plus stores and category. Neither the website
	// nor webye's own form can ask for more, and each list in use costs
	// one more count query, so any group beyond this is ignored.
	maxGrupos = 13

	// The form sends the price as two fields; the handler turns them into
	// the website's sellPrice=min:max.
	campoPrecioMin = "precioMin"
	campoPrecioMax = "precioMax"
)

// The website shows its product attributes with their own Spanish names,
// starting with a capital letter (Clasificación PEGI, Grado, Color…), and
// keeps its internal fields in lowerCamelCase (categoryId, scId…). Only
// the former, plus stores and category, can be filtered from webye.
var reAtributo = regexp.MustCompile(`^\p{Lu}[\p{L}\p{N} ]{0,63}$`)

func esFiltrable(atributo string) bool {
	return atributo == atributoTiendas || atributo == atributoCategoria || reAtributo.MatchString(atributo)
}

type grupoFiltro struct {
	Atributo string
	Valores  []string
}

type filtros struct {
	Agotados  bool
	PrecioMin string // a plain number, or "" for no limit
	PrecioMax string
	Grupos    []grupoFiltro // in canonical order, see ordenNatural
}

func (f filtros) activos() bool {
	return f.Agotados || f.PrecioMin != "" || f.PrecioMax != "" || len(f.Grupos) > 0
}

// cuantos is how many filters are in use, for the panel title.
func (f filtros) cuantos() int {
	n := len(f.Grupos)
	if f.Agotados {
		n++
	}
	if f.PrecioMin != "" || f.PrecioMax != "" {
		n++
	}
	return n
}

func (f filtros) marcados(atributo string) []string {
	for _, g := range f.Grupos {
		if g.Atributo == atributo {
			return g.Valores
		}
	}
	return nil
}

// leerFiltros reads the filters of a URL, either in the website's shape or
// as sent by webye's form (repeated parameters and two price fields).
// Anything that is not a filter the website offers is ignored.
func leerFiltros(q url.Values) filtros {
	var f filtros
	f.Agotados = q.Get("availability") == "allStock"

	minimo, maximo, _ := strings.Cut(q.Get("sellPrice"), ":")
	if q.Has(campoPrecioMin) || q.Has(campoPrecioMax) {
		minimo, maximo = q.Get(campoPrecioMin), q.Get(campoPrecioMax)
	}
	f.PrecioMin, f.PrecioMax = leerPrecio(minimo), leerPrecio(maximo)
	if f.PrecioMin != "" && f.PrecioMax != "" {
		a, _ := strconv.ParseFloat(f.PrecioMin, 64)
		b, _ := strconv.ParseFloat(f.PrecioMax, 64)
		if a > b {
			f.PrecioMin, f.PrecioMax = f.PrecioMax, f.PrecioMin
		}
	}

	var atributos []string
	for a := range q {
		if esFiltrable(a) {
			atributos = append(atributos, a)
		}
	}
	slices.SortFunc(atributos, ordenNatural)
	for _, a := range atributos {
		if len(f.Grupos) == maxGrupos {
			break
		}
		visto := map[string]bool{}
		var valores []string
		for _, crudo := range q[a] {
			for _, v := range strings.Split(crudo, separadorValores) {
				v = strings.TrimSpace(v)
				// A leading "-" would make Algolia exclude the value
				// instead of asking for it.
				if v == "" || strings.HasPrefix(v, "-") || visto[v] || len(valores) == maxValoresFaceta {
					continue
				}
				visto[v] = true
				valores = append(valores, v)
			}
		}
		if len(valores) > 0 {
			slices.SortFunc(valores, ordenNatural)
			f.Grupos = append(f.Grupos, grupoFiltro{Atributo: a, Valores: valores})
		}
	}
	return f
}

// leerPrecio accepts "15", "9.5" and "9,5"; anything else, or a price that
// could not narrow the search, means no limit.
func leerPrecio(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 || math.IsInf(v, 0) || math.IsNaN(v) {
		return ""
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// desdeFormulario tells a submission of webye's form, which is redirected
// to the website's URL shape, from a link already in that shape.
func desdeFormulario(q url.Values) bool {
	if q.Has(campoPrecioMin) || q.Has(campoPrecioMax) {
		return true
	}
	for a, v := range q {
		if esFiltrable(a) && len(v) > 1 {
			return true
		}
	}
	return false
}

// parametros writes the filters as URL parameters, in the website's shape
// and in a fixed order.
func (f filtros) parametros() string {
	var b strings.Builder
	if f.Agotados {
		b.WriteString("&availability=allStock")
	}
	if f.PrecioMin != "" || f.PrecioMax != "" {
		b.WriteString("&sellPrice=" + f.PrecioMin + ":" + f.PrecioMax)
	}
	for _, g := range f.Grupos {
		valores := make([]string, len(g.Valores))
		for i, v := range g.Valores {
			valores[i] = url.QueryEscape(v)
		}
		b.WriteString("&" + url.QueryEscape(g.Atributo) + "=" + strings.Join(valores, separadorValores))
	}
	return b.String()
}

// facetFilters returns the list filters for Algolia, leaving out the list
// named sin, for its own count query.
func (f filtros) facetFilters(sin string) [][]string {
	var grupos [][]string
	for _, g := range f.Grupos {
		if g.Atributo == sin {
			continue
		}
		grupo := make([]string, len(g.Valores))
		for i, v := range g.Valores {
			grupo[i] = g.Atributo + ":" + v
		}
		grupos = append(grupos, grupo)
	}
	return grupos
}

func (f filtros) numericos() []string {
	var n []string
	if f.PrecioMin != "" {
		n = append(n, "sellPrice>="+f.PrecioMin)
	}
	if f.PrecioMax != "" {
		n = append(n, "sellPrice<="+f.PrecioMax)
	}
	return n
}

// ---------- the filter panel ----------

type opcionFiltro struct {
	Valor   string
	Cuenta  int
	Marcado bool
}

type grupoVista struct {
	Atributo string
	Etiqueta string
	Marcados int
	Opciones []opcionFiltro
}

type vistaFiltros struct {
	Consulta  string
	Orden     string
	Activos   int
	Agotados  bool
	PrecioMin string
	PrecioMax string
	Grupos    []grupoVista
	Quitar    string // the same search without filters
}

// etiquetas are the website's titles for the lists that are not product
// attributes; attributes are shown by their own name.
var etiquetas = map[string]string{
	atributoTiendas:   "Tiendas",
	atributoCategoria: "Por Categoria",
}

// vistaDeFiltros builds the panel: stores and category first, then the
// product attributes, as on the website. Ticked values always show, even
// when no product is left with them, so they can be unticked.
func vistaDeFiltros(consulta, orden string, f filtros, facetas map[string]map[string]int) *vistaFiltros {
	v := &vistaFiltros{
		Consulta:  consulta,
		Orden:     orden,
		Activos:   f.cuantos(),
		Agotados:  f.Agotados,
		PrecioMin: f.PrecioMin,
		PrecioMax: f.PrecioMax,
		Quitar:    enlaceBusqueda(consulta, orden, filtros{}, 1),
	}
	var atributos []string
	for a := range facetas {
		if esFiltrable(a) {
			atributos = append(atributos, a)
		}
	}
	for _, g := range f.Grupos {
		if _, ok := facetas[g.Atributo]; !ok {
			atributos = append(atributos, g.Atributo)
		}
	}
	slices.SortFunc(atributos, func(a, b string) int {
		return cmp.Or(cmp.Compare(prioridad(a), prioridad(b)), ordenNatural(a, b))
	})
	for _, a := range atributos {
		marcados := f.marcados(a)
		g := grupoVista{Atributo: a, Etiqueta: cmp.Or(etiquetas[a], a), Marcados: len(marcados)}
		for valor, n := range facetas[a] {
			g.Opciones = append(g.Opciones, opcionFiltro{Valor: valor, Cuenta: n, Marcado: slices.Contains(marcados, valor)})
		}
		for _, m := range marcados {
			if _, ok := facetas[a][m]; !ok {
				g.Opciones = append(g.Opciones, opcionFiltro{Valor: m, Marcado: true})
			}
		}
		// Ticked values first, so they are in sight even in a long list.
		slices.SortFunc(g.Opciones, func(x, y opcionFiltro) int {
			return cmp.Or(cmp.Compare(sinMarcar(x), sinMarcar(y)), ordenNatural(x.Valor, y.Valor))
		})
		if len(g.Opciones) > 0 {
			v.Grupos = append(v.Grupos, g)
		}
	}
	return v
}

func sinMarcar(o opcionFiltro) int {
	if o.Marcado {
		return 0
	}
	return 1
}

func prioridad(atributo string) int {
	switch atributo {
	case atributoTiendas:
		return 0
	case atributoCategoria:
		return 1
	}
	return 2
}

// ---------- ordering ----------

var plegado = strings.NewReplacer(
	"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u",
	"à", "a", "è", "e", "ì", "i", "ò", "o", "ù", "u", "ç", "c",
)

// ordenNatural orders like the website's lists: alphabetically, ignoring
// case and accents, with numbers by their value, so 3+ comes before 12+.
func ordenNatural(a, b string) int {
	x, y := plegado.Replace(strings.ToLower(a)), plegado.Replace(strings.ToLower(b))
	for x != "" && y != "" {
		if esCifra(x[0]) && esCifra(y[0]) {
			nx, restoX := cortarNumero(x)
			ny, restoY := cortarNumero(y)
			if c := cmp.Or(cmp.Compare(len(nx), len(ny)), strings.Compare(nx, ny)); c != 0 {
				return c
			}
			x, y = restoX, restoY
			continue
		}
		if x[0] != y[0] {
			return cmp.Compare(x[0], y[0])
		}
		x, y = x[1:], y[1:]
	}
	// Equal once folded: still a fixed order.
	return cmp.Or(cmp.Compare(len(x), len(y)), strings.Compare(a, b))
}

func esCifra(c byte) bool { return '0' <= c && c <= '9' }

// cortarNumero splits the leading digits of s, without leading zeros.
func cortarNumero(s string) (string, string) {
	i := 0
	for i < len(s) && esCifra(s[i]) {
		i++
	}
	n := strings.TrimLeft(s[:i], "0")
	return n, s[i:]
}
