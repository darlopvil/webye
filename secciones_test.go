package main

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Trimmed copies of real wss2 answers.
const (
	supercatsCeX    = `{"response":{"ack":"Success","data":{"superCats":[{"superCatId":1,"superCatFriendlyName":"Juegos","showOnWebsiteBanner":1},{"superCatId":4,"superCatFriendlyName":"Móviles","showOnWebsiteBanner":1}]},"error":{}}}`
	lineasCeX       = `{"response":{"ack":"Success","data":{"productLines":[{"superCatId":1,"productLineId":79,"productLineName":"3DS","totalCategories":3},{"superCatId":1,"productLineId":82,"productLineName":"PlayStation 4","totalCategories":4}],"cacheUpdatedTime":"2026-11-18 18:41:39"},"error":{}}}`
	categoriasCeX   = `{"response":{"ack":"Success","data":{"categories":[{"superCatId":1,"categoryId":1001,"categoryName":"PS4 Juegos","categoryFriendlyName":"PS4 Juegos","productLineId":82,"totalBoxes":2637},{"superCatId":1,"categoryId":1124,"categoryName":"PS4 Mandos","categoryFriendlyName":"PS4 Mandos","productLineId":82,"totalBoxes":296}],"cacheUpdatedTime":"2026-11-18 18:41:39"},"error":{}}}`
	seccionInvalida = `{"response":{"ack":"Failure","data":"","error":{"code":1001,"internal_message":"Invalid SuperCatId","moreInfo":[]}}}`
)

// wss2DeSecciones answers like wss2 for the section lists and keeps every
// path and query it is asked for.
func wss2DeSecciones(t *testing.T) *[]string {
	t.Helper()
	var pedidas []string
	wss2DePrueba(t, func(w http.ResponseWriter, r *http.Request) {
		pedidas = append(pedidas, r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v3/supercats":
			w.Write([]byte(supercatsCeX))
		case r.URL.Path == "/v3/productlines" && r.URL.Query().Get("superCatId") == "1":
			w.Write([]byte(lineasCeX))
		case r.URL.Path == "/v3/categories" && r.URL.Query().Get("productLineIds") == "[82]":
			w.Write([]byte(categoriasCeX))
		default:
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(seccionInvalida))
		}
	})
	return &pedidas
}

func ponerSecciones(t *testing.T, s []superCat) {
	t.Helper()
	antes := secciones.Load()
	secciones.Store(&s)
	t.Cleanup(func() { secciones.Store(antes) })
}

func TestTheSectionsAreKeptInTheBackground(t *testing.T) {
	wss2DeSecciones(t)
	ponerSecciones(t, nil)
	if espera := refrescarSecciones(context.Background()); espera != ttlSecciones {
		t.Fatalf("next refresh in %s, want %s", espera, ttlSecciones)
	}
	if s := seccionesActuales(); !reflect.DeepEqual(s, []superCat{{1, "Juegos"}, {4, "Móviles"}}) {
		t.Fatalf("got %+v", s)
	}
	// A failure keeps the last good list and retries soon.
	wss2DePrueba(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	if espera := refrescarSecciones(context.Background()); espera != esperaSecciones || len(seccionesActuales()) != 2 {
		t.Fatalf("after a failure: next refresh in %s, %d sections", espera, len(seccionesActuales()))
	}
}

func TestEveryPageShowsTheSectionsWithoutAskingCeX(t *testing.T) {
	pedidas := wss2DeSecciones(t)
	ponerSecciones(t, []superCat{{1, "Juegos"}, {4, "Móviles"}})
	for _, ruta := range []string{"/", "/about"} {
		html := pedirRuta(t, ruta).Body.String()
		if !strings.Contains(html, `<nav class="secciones"`) || !strings.Contains(html, `<a href="/supercat?superCatId=4&amp;superCatName=M%c3%b3viles">Móviles</a>`) {
			t.Errorf("%s: no sections bar", ruta)
		}
	}
	// The bar is enough: the home page does not repeat them as tiles.
	if strings.Contains(pedirRuta(t, "/").Body.String(), `class="baldosas`) {
		t.Error("the home page repeats the sections as tiles")
	}
	if len(*pedidas) != 0 {
		t.Fatalf("pages asked CeX for the sections: %v", *pedidas)
	}
	// Without them, the bar is just not drawn.
	ponerSecciones(t, nil)
	if strings.Contains(pedirRuta(t, "/about").Body.String(), `class="secciones"`) {
		t.Error("an empty sections bar was drawn")
	}
}

func TestASupercategoryListsItsProductLines(t *testing.T) {
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	wss2DeSecciones(t)
	w := pedirRuta(t, "/supercat?superCatId=1&superCatName=Juegos")
	html := w.Body.String()
	for _, debe := range []string{
		`<h1 class="titulo-seccion">Juegos</h1>`,
		`<a href="/search/?productLineId=82&amp;productLineName=PlayStation&#43;4">PlayStation 4</a>`,
		`<a href="/search/?productLineId=79&amp;productLineName=3DS">3DS</a>`,
	} {
		if w.Code != http.StatusOK || !strings.Contains(html, debe) {
			t.Errorf("%d, missing %q", w.Code, debe)
		}
	}
}

func TestUnknownSupercategories(t *testing.T) {
	limitePaginas = nuevoLimitador(rafagaPaginas, ritmoPaginas)
	pedidas := wss2DeSecciones(t)
	if w := pedirRuta(t, "/supercat?superCatId=99"); w.Code != http.StatusNotFound {
		t.Errorf("unknown supercategory: %d", w.Code)
	}
	for _, id := range []string{"", "abc", "-1", "0", "1%20OR%201"} {
		if w := pedirRuta(t, "/supercat?superCatId="+id); w.Code != http.StatusNotFound {
			t.Errorf("superCatId=%q: %d", id, w.Code)
		}
	}
	if len(*pedidas) != 1 {
		t.Fatalf("invalid IDs reached CeX: %v", *pedidas)
	}
}

func TestAProductLineIsASearchWithoutText(t *testing.T) {
	recibidas := algoliaConFacetas(t, 2, nil, nil)
	wss2DeSecciones(t)
	html := pedirRuta(t, "/search?productLineId=82&productLineName=PlayStation%204").Body.String()
	p := (*recibidas)[0][0]
	if !reflect.DeepEqual(p.NumericFilters, []string{"productLineId=82"}) || p.FacetFilters != nil {
		t.Errorf("numericFilters %v, facetFilters %v", p.NumericFilters, p.FacetFilters)
	}
	for _, debe := range []string{
		`<h1 class="titulo-seccion">PlayStation 4</h1>`,
		`resultados en «PlayStation 4»`,
		// Its categories, with the website's own links.
		`<a href="/search/?categoryIds=1001&amp;categoryName=PS4&#43;Juegos">PS4 Juegos</a>`,
		// Sorting keeps the product line.
		`<a href="/search/?productLineId=82&amp;productLineName=PlayStation&#43;4&amp;sort=price_asc">`,
	} {
		if !strings.Contains(html, debe) {
			t.Errorf("missing %q", debe)
		}
	}
}

func TestACategoryIsASearchWithoutText(t *testing.T) {
	recibidas := algoliaConFacetas(t, 2, nil, nil)
	pedidas := wss2DeSecciones(t)
	html := pedirRuta(t, "/search?categoryIds=1001&categoryName=PS4%20Juegos").Body.String()
	p := (*recibidas)[0][0]
	if !reflect.DeepEqual(p.FacetFilters, [][]string{{"categoryId:1001"}}) || p.NumericFilters != nil {
		t.Errorf("facetFilters %v, numericFilters %v", p.FacetFilters, p.NumericFilters)
	}
	if !strings.Contains(html, `<h1 class="titulo-seccion">PS4 Juegos</h1>`) {
		t.Error("no title")
	}
	if len(*pedidas) != 0 {
		t.Errorf("a category asked wss2 for something: %v", *pedidas)
	}
}

func TestTextAndFiltersWithinACategory(t *testing.T) {
	recibidas := algoliaConFacetas(t, 2, nil, nil)
	wss2DeSecciones(t)
	html := pedirRuta(t, "/search?stext=fifa&categoryIds=1001&categoryName=PS4%20Juegos&Grado=A").Body.String()
	c := (*recibidas)[0]
	if len(c) != 2 || !reflect.DeepEqual(c[0].FacetFilters, [][]string{{"Grado:A"}, {"categoryId:1001"}}) ||
		!reflect.DeepEqual(c[1].FacetFilters, [][]string{{"categoryId:1001"}}) {
		t.Fatalf("queries %+v", c)
	}
	for _, debe := range []string{
		`resultados para «fifa» en «PS4 Juegos»`,
		`<input type="hidden" name="categoryIds" value="1001">`,
		`<input type="hidden" name="categoryName" value="PS4 Juegos">`,
		`<a href="/search/?stext=fifa&amp;categoryIds=1001&amp;categoryName=PS4&#43;Juegos">Quitar filtros</a>`,
	} {
		if !strings.Contains(html, debe) {
			t.Errorf("missing %q", debe)
		}
	}
	// The filter form keeps the category.
	w := pedirRuta(t, "/search/?categoryIds=1001&categoryName=PS4+Juegos&precioMin=10&precioMax=")
	if hacia := "/search/?categoryIds=1001&categoryName=PS4+Juegos&sellPrice=10:"; w.Header().Get("Location") != hacia {
		t.Fatalf("form went to %q, want %q", w.Header().Get("Location"), hacia)
	}
}

func TestJunkSectionIDsAreIgnored(t *testing.T) {
	recibidas := algoliaConFacetas(t, 1, nil, nil)
	wss2DeSecciones(t)
	if w := pedirRuta(t, "/search?productLineId=abc&categoryIds=x,-3,"); w.Code != http.StatusSeeOther {
		t.Fatalf("a search with nothing valid should go home, got %d", w.Code)
	}
	pedirRuta(t, "/search?categoryIds=1124,x,1001,1001")
	if p := (*recibidas)[0][0]; !reflect.DeepEqual(p.FacetFilters, [][]string{{"categoryId:1001", "categoryId:1124"}}) {
		t.Fatalf("facetFilters %v", p.FacetFilters)
	}
}

func TestNamesFromTheURLAreCut(t *testing.T) {
	if n := nombreDeURL(strings.Repeat("á", 200)); len([]rune(n)) != largoNombre {
		t.Fatalf("%d runes", len([]rune(n)))
	}
	if n := nombreDeURL("  PS4   Juegos "); n != "PS4 Juegos" {
		t.Fatalf("%q", n)
	}
}
